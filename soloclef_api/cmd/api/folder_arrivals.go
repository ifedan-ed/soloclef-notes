package main

import (
	"context"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/eventstream"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
)

// eventPublisher is the one call of the event stream this adapter makes
// (*eventstream.Publisher).
type eventPublisher interface {
	Publish(ctx context.Context, event eventstream.Event) (string, error)
}

// folderArrivals tells the event stream that a ready document was filed in
// a folder. It is the join between two packages that must not know each
// other: internal/folders decides what an arrival is and names no
// transport, and internal/eventstream carries events and knows nothing of
// folders. The event belongs to the account the document belongs to.
type folderArrivals struct {
	events eventPublisher
}

func (a folderArrivals) DocumentEnteredFolder(ctx context.Context, arrival folders.Arrival, at time.Time) error {
	event, err := eventstream.DocumentEnteredFolderEvent(arrival.UserID, eventstream.DocumentEnteredFolder{
		DocumentID:       arrival.DocumentID,
		FolderID:         arrival.FolderID,
		PreviousFolderID: arrival.PreviousFolderID,
		DocumentType:     arrival.DocumentType,
		Title:            arrival.Title,
		ConnectorID:      arrival.ConnectorID,
	}, at)
	if err != nil {
		return err
	}
	_, err = a.events.Publish(ctx, event)
	return err
}
