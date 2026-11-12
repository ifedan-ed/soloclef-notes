package searchconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
)

// DeletedEvent is the product event a removed connection owes.
const DeletedEvent = "connector_deleted"

// What the connection delete says, beside the package's other wording.
const (
	// removalUnfinished is the answer when the removal stopped partway.
	// The connection's documents are already out of search, the connection
	// is still listed, and the same request finishes the job.
	removalUnfinished = "The connection could not be fully removed right now. Delete it again to finish."
)

// ConnectionReader reads the connection a delete is about (Store): the
// caller's own, or nil.
type ConnectionReader interface {
	Connection(ctx context.Context, ownerID string, connectorID int64) (*Connection, error)
}

// ConnectionRemovals is the database as a connection's removal uses it
// (Store). Each call is its own transaction: every step is committed before
// the next begins, which is what lets a repeated delete carry on from where
// an earlier one stopped. Every call names the owner, so a removal can only
// ever touch the caller's own connection and documents.
type ConnectionRemovals interface {
	// MarkDocumentsDeleting takes every document of the connection out of
	// search, before any is removed.
	MarkDocumentsDeleting(ctx context.Context, ownerID string, connectorID int64) error
	// DocumentIDs is up to `batch` of the connection's remaining documents.
	DocumentIDs(ctx context.Context, ownerID string, connectorID int64, batch int) ([]int64, error)
	// DeleteConnection deletes the row, and reports false when it was
	// already gone.
	DeleteConnection(ctx context.Context, ownerID string, connectorID int64) (bool, error)
}

// DocumentRemover removes a document and everything stored for it
// (documentdeletion.Removal).
type DocumentRemover interface {
	Remove(ctx context.Context, documentID int64) error
}

// Revoker ends a connection's credential at its provider, best effort
// (connectionrevocation.Revocation).
type Revoker interface {
	Revoke(ctx context.Context, connectorID int64, connectorType string, config []byte)
}

// The pace of a removal.
const (
	// removalBatch is how many of a connection's documents are read at a
	// time.
	removalBatch = 200
	// removalWorkers is how many documents are removed at once. Each
	// removal is a few statements and a request to the vector store and to
	// the file store, so it spends its time waiting. Measured against real
	// Postgres, Qdrant and MinIO on 2026-11-11, a connection of 201
	// documents (2,000 chunks, 2,000 points, 200 stored files) took 1.79 s
	// with one worker, 0.75 s with four and 0.31 s with eight
	// (internal/documentlifecycle, TestRemovingAConnectionAgainstRealServices).
	// Four, not eight: each worker holds a database connection while it
	// works, and a replica's pool is ten.
	removalWorkers = 4
	// removalLimit bounds the whole removal. It runs outside the request's
	// context (see serve), so it needs a limit of its own; a connection of
	// ten thousand documents is well inside it.
	removalLimit = 15 * time.Minute
)

// ConnectionDeleteRoute removes a connection of any kind and every document
// that came through it: DELETE /search-source-connectors/{connector_id}.
//
// # What a removal does, and in what order
//
//  1. Every document of the connection is marked "deleting", in one
//     statement. From that instant none of them is in search.
//  2. Each document is removed with the document delete's own removal
//     (documentdeletion.Removal): its points from the vector store, its
//     chunks, its stored files, its row.
//  3. The connection's credential is ended at its provider, best effort
//     (Revoker): a Google grant is revoked, a Nextcloud app password
//     deleted.
//  4. The connection's row is deleted, and the product event recorded.
//  5. For an MCP server, the account's MCP counter is moved (Tools).
//
// The row is last because it is what a person deletes again: a removal that
// stops anywhere before it leaves the connection listed, its remaining
// documents hidden, and the same request finishes the job. The credential
// is ended just before the row goes, not first, so that a removal that
// fails early does not leave a listed connection whose credential is
// already dead.
//
// # Why it is shaped this way
//
//   - Each document is removed whole. Deleting the rows and letting the
//     cascade take the chunks would leave every vector in the vector store
//     and every file in the bucket for a sweeper to find.
//   - Every document leaves search before the first is removed, not when its
//     batch is reached.
//   - It can be repeated usefully: a removal that stops partway answers 503
//     with a sentence, and asking again carries on.
//
// Who may remove a connection is one rule: its owner. The connection is read
// with the caller as its owner, so another account's is "not found" before
// anything is marked, removed or revoked.
type ConnectionDeleteRoute struct {
	Connections ConnectionReader
	Removals    ConnectionRemovals
	Documents   DocumentRemover
	Revocation  Revoker
	// Tools is told once an MCP server is gone.
	Tools     ToolChanges
	Analytics routeanalytics.Capture
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
}

// Register adds the operation.
func (r ConnectionDeleteRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-search-source-connector",
		Method:      http.MethodDelete,
		Path:        OnePath,
		Summary:     "Remove a connection and the documents that came through it",
		Description: "Removes every document of the connection (vectors, chunks, stored files and rows), " +
			"ends its credential at the provider where there is one, and deletes the connection. " +
			"Answers once it is gone; safe to repeat.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct {
		ConnectorID string `path:"connector_id" doc:"The connection's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

// connectionDeleted is the response body, with the fields the web app has
// always been sent.
type connectionDeleted struct {
	Message          string `json:"message"`
	Status           string `json:"status"`
	ConnectorID      int64  `json:"connector_id"`
	ConnectorName    string `json:"connector_name"`
	DocumentsDeleted int64  `json:"documents_deleted"`
}

func (r ConnectionDeleteRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the connection could not be removed"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.caught(noCaller, nil)
		return
	}

	connectorID, failure, rangeErr := requestnumber.Parse(ctx.Param("connector_id"), "path", "connector_id")
	if failure != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{*failure})
		return
	}
	// A number too wide for the column names no connection, and is answered
	// as any other id nobody has.
	if rangeErr != nil {
		r.Errors.Write(response, http.StatusNotFound, connectorAbsent, requestID)
		return
	}

	connection, err := r.Connections.Connection(ctx.Context(), caller.UserID, connectorID)
	if err != nil {
		failed.caught(rowUnread, err)
		return
	}
	if connection == nil {
		r.Errors.Write(response, http.StatusNotFound, connectorAbsent, requestID)
		return
	}

	// The removal can take longer than the server lets a response wait
	// (cmd/api's write timeout), so the answer's deadline is moved to the
	// removal's own limit. If it cannot be moved the removal still runs to
	// its end; only the answer may be lost, and the delete can be repeated.
	if err := http.NewResponseController(response).SetWriteDeadline(time.Now().Add(removalLimit + time.Minute)); err != nil {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelWarn, "the answer's write deadline could not be moved for a connection's removal",
			slog.String("request_id", requestID), slog.String("error", err.Error()))
	}

	// Not the request's context. A removal that has begun should finish
	// even if the person closes the tab: one that stopped is repaired by
	// asking again, but nobody asks again for a request they walked away
	// from.
	removing, done := context.WithTimeout(context.WithoutCancel(ctx.Context()), removalLimit)
	defer done()
	started := time.Now()
	removed, err := r.remove(removing, *connection)
	if err != nil {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError,
			"a connection was not fully removed; its documents are marked and the delete can be repeated",
			slog.String("request_id", requestID), slog.Int64("connector_id", connectorID),
			slog.Int64("documents_removed", removed), slog.Duration("after", time.Since(started)),
			slog.String("error", err.Error()))
		r.Errors.Write(response, http.StatusServiceUnavailable, removalUnfinished, requestID)
		return
	}

	// The row is gone and that is committed. What follows cannot undo it.
	r.record(caller, *connection)
	if connection.IsServer() {
		toolsChanged(ctx.Context(), r.Tools, r.Logger, requestID, caller.UserID)
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "connection removed",
		slog.String("request_id", requestID), slog.Int64("connector_id", connectorID),
		slog.String("connector_type", connection.Type), slog.Int64("documents", removed),
		slog.Duration("took", time.Since(started)))

	noun := "documents"
	if removed == 1 {
		noun = "document"
	}
	body, err := json.Marshal(connectionDeleted{
		Message:          fmt.Sprintf("Connector '%s' deleted. %d %s removed.", connection.Name, removed, noun),
		Status:           "completed",
		ConnectorID:      connectorID,
		ConnectorName:    connection.Name,
		DocumentsDeleted: removed,
	})
	if err != nil {
		failed.caught(bodyUnwritten, err)
		return
	}
	header := response.Header()
	header.Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(body)
}

// remove is steps 1 to 4, and reports how many documents it removed.
func (r ConnectionDeleteRoute) remove(ctx context.Context, connection Connection) (int64, error) {
	if err := r.Removals.MarkDocumentsDeleting(ctx, connection.UserID, connection.ID); err != nil {
		return 0, fmt.Errorf("marking the connection's documents: %w", err)
	}

	var removed int64
	// The lowest id of the batch before, to notice a removal that reports
	// success and leaves its documents: without it this loop would read
	// the same batch for ever.
	var last int64
	for {
		ids, err := r.Removals.DocumentIDs(ctx, connection.UserID, connection.ID, removalBatch)
		if err != nil {
			return removed, fmt.Errorf("reading the connection's documents: %w", err)
		}
		if len(ids) == 0 {
			break
		}
		if ids[0] == last {
			return removed, errNoDocumentsRemoved
		}
		last = ids[0]
		count, err := r.removeBatch(ctx, ids)
		removed += count
		if err != nil {
			// Stop at the first batch that did not empty: asking for the
			// next would return the same documents.
			return removed, err
		}
	}

	// The credential, read from the row that is about to go. The config is
	// rendered back to JSON from what was read; a config that cannot be
	// rendered holds no credential this tier could have used.
	if config, err := pythonjson.Marshal(connection.Config); err == nil {
		r.Revocation.Revoke(ctx, connection.ID, connection.Type, config)
	} else {
		r.Logger.LogAttrs(ctx, slog.LevelWarn, "a removed connection's stored settings could not be read to end its credential",
			slog.Int64("connector_id", connection.ID), slog.String("error", err.Error()))
	}

	// False is another request having deleted the row first: it is gone,
	// which is what was asked.
	if _, err := r.Removals.DeleteConnection(ctx, connection.UserID, connection.ID); err != nil {
		return removed, fmt.Errorf("deleting the connection: %w", err)
	}
	return removed, nil
}

// removeBatch removes these documents, removalWorkers at a time, and
// reports how many were removed and the first failure.
func (r ConnectionDeleteRoute) removeBatch(ctx context.Context, ids []int64) (int64, error) {
	var (
		wait    sync.WaitGroup
		mu      sync.Mutex
		removed int64
		first   error
	)
	next := make(chan int64)
	for worker := 0; worker < min(removalWorkers, len(ids)); worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for id := range next {
				err := r.Documents.Remove(ctx, id)
				mu.Lock()
				if err == nil {
					removed++
				} else if first == nil {
					first = fmt.Errorf("removing document %d: %w", id, err)
				}
				mu.Unlock()
			}
		}()
	}
	for _, id := range ids {
		next <- id
	}
	close(next)
	wait.Wait()
	if first == nil && ctx.Err() != nil {
		first = ctx.Err()
	}
	return removed, first
}

// record captures the product event: the connection's id and type,
// attributed to the caller.
func (r ConnectionDeleteRoute) record(caller principal.Principal, connection Connection) {
	r.Analytics.Record(caller, DeletedEvent, func(properties *pythonjson.Object) {
		properties.Set("connector_id", connection.ID)
		properties.Set("connector_type", connection.Type)
	})
}

var errNoDocumentsRemoved = errors.New("searchconnectors: a batch of documents was reported removed and is still there")
