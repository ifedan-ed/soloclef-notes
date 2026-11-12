package folders

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// readyState is the only state an arrival is announced for. A document
// still being indexed, one that failed and one being deleted are moved and
// not announced: whatever is waiting for a document to arrive is waiting
// for one it can read.
const readyState = "ready"

// Arrival is a ready document that has just been filed in a folder.
type Arrival struct {
	// UserID is the account the document, and both folders, belong to.
	UserID     string
	DocumentID int64
	// FolderID is the folder it arrived in.
	FolderID int64
	// PreviousFolderID is where it was, or nil if it was in no folder.
	PreviousFolderID *int64
	DocumentType     string
	Title            string
	// ConnectorID is the connection it came through, if it came through one.
	ConnectorID *int64
}

// Arrivals is told of each arrival once the move is stored. It is this
// package's own seam and names no transport: cmd/api adapts the event
// stream to it. A deployment with nothing listening passes nil, and the
// moves then write the rows and announce nothing.
type Arrivals interface {
	DocumentEnteredFolder(ctx context.Context, arrival Arrival, at time.Time) error
}

// arrivalOf decides whether filing this document in folderID is an arrival.
// Three things make it not one: the document is already in that folder; it
// is being taken out of every folder, so there is nowhere it arrived; or it
// is not ready.
func arrivalOf(userID string, document Document, folderID *int64) (Arrival, bool) {
	if folderID == nil || sameFolder(document.FolderID, folderID) {
		return Arrival{}, false
	}
	if documentState(document.Status) != readyState {
		return Arrival{}, false
	}
	return Arrival{
		UserID:           userID,
		DocumentID:       document.ID,
		FolderID:         *folderID,
		PreviousFolderID: document.FolderID,
		DocumentType:     document.DocumentType,
		Title:            document.Title,
		ConnectorID:      document.ConnectorID,
	}, true
}

// arrivals is the same decision over several documents, in the order given,
// which is id order because the read asks for it: one move is announced in
// the same order every time.
func arrivals(userID string, documents []Document, folderID *int64) []Arrival {
	var announced []Arrival
	for _, document := range documents {
		if arrival, owed := arrivalOf(userID, document, folderID); owed {
			announced = append(announced, arrival)
		}
	}
	return announced
}

// moved is the ids whose folder actually changes. A document already where
// it is being sent is left out, so the statement writes only rows that
// change.
func moved(documents []Document, folderID *int64) []int64 {
	var ids []int64
	for _, document := range documents {
		if !sameFolder(document.FolderID, folderID) {
			ids = append(ids, document.ID)
		}
	}
	return ids
}

// documentState is the `state` of a status column, or "" for a status that
// carries none it can read.
func documentState(status []byte) string {
	var read struct {
		State string `json:"state"`
	}
	if json.Unmarshal(status, &read) != nil {
		return ""
	}
	return read.State
}

// announce tells Arrivals of each arrival. It is called only after the move
// is stored, so an arrival is never announced for a move that did not
// happen. An announcement that fails is logged and the move stands: the
// document is where the person put it, and that is what they asked for.
// The arrivals of one move share one time, because they are one move.
func announce(ctx context.Context, events Arrivals, logger *slog.Logger, announced []Arrival, at time.Time, requestID string) {
	if events == nil {
		return
	}
	// The move is stored, so a client that has gone away does not unsay it.
	ctx = context.WithoutCancel(ctx)
	for _, arrival := range announced {
		if err := events.DocumentEnteredFolder(ctx, arrival, at); err != nil {
			logger.LogAttrs(ctx, slog.LevelError,
				"could not announce a document's arrival in a folder",
				slog.String("request_id", requestID),
				slog.Int64("document_id", arrival.DocumentID),
				slog.String("error", err.Error()))
		}
	}
}
