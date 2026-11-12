package documents

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/indexqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// Restores reads and writes what the restore does.
type Restores interface {
	Restorable(ctx context.Context, documentID int64, userID string) (*Restorable, error)
	Version(ctx context.Context, documentID, number int64) (*Version, error)
	Restore(ctx context.Context, work RestoreWork) error
}

// RestoreRoute serves
// POST /documents/{document_id}/versions/{version_number}/restore
// (restore_document_version): keep the document's current state as a
// version, put an older version's text back on it, and queue the reindex.
//
// The document is read for the caller: another account's document is the
// 404 an unused id gets, and the write names the owner again.
//
// # After the commit
//
// The indexing job (ReindexOf) follows the commit and is inside no try. A
// broker that is down is therefore a 500 -- the uncaught kind -- with the
// restore already committed and nothing queued to reindex it. That is not a
// database.Effects case: an effect cannot fail the request, and this one
// does.
type RestoreRoute struct {
	Restores Restores
	Queue    celeryqueue.Enqueuer
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	// Now is the clock the snapshot is stamped and aged by. The Python
	// helper reads the application's, not the database's.
	Now func() time.Time
}

func (r RestoreRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "restore-document-version",
		Method:      http.MethodPost,
		Path:        RestorePath,
		Summary:     "Put a version's text back on its document",
		Description: "The same response as the Python tier's " +
			"POST /api/v1/documents/{document_id}/versions/{version_number}/restore, " +
			"byte for byte, and the same rows; the restored text is indexed by the Go indexer.",
		Tags: []string{"documents"},
	}, func(ctx context.Context, _ *struct {
		DocumentID    string `path:"document_id" doc:"The document's id"`
		VersionNumber string `path:"version_number" doc:"The version's number"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r RestoreRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, what: "a version of a document could not be restored"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// write if the wiring is ever wrong.
		failed.uncaught(noCaller, nil)
		return
	}

	invalid := make([]errorenvelope.Failure, 0, 2)
	documentID, _ := readNumber(ctx.Param("document_id"), &invalid, "path", "document_id")
	versionNumber, _ := readNumber(ctx.Param("version_number"), &invalid, "path", "version_number")
	if len(invalid) > 0 {
		r.Errors.WriteValidation(response, requestID, invalid)
		return
	}

	if documentID.tooWide() {
		failed.uncaught(documentTooWide, errTooWide)
		return
	}
	document, err := r.Restores.Restorable(ctx.Context(), documentID.value, caller.UserID)
	if err != nil {
		failed.uncaught(documentUnread, err)
		return
	}
	if document == nil {
		r.Errors.Write(response, http.StatusNotFound, documentAbsent, requestID)
		return
	}

	if versionNumber.tooWide() {
		failed.uncaught(versionTooWide, errTooWide)
		return
	}
	version, err := r.Restores.Version(ctx.Context(), documentID.value, versionNumber.value)
	if err != nil {
		failed.uncaught("could not read the version", err)
		return
	}
	if version == nil {
		r.Errors.Write(response, http.StatusNotFound, versionAbsent, requestID)
		return
	}

	if err := r.Restores.Restore(ctx.Context(), RestoreWork{
		Document: *document,
		Version:  *version,
		Now:      r.Now(),
	}); err != nil {
		failed.uncaught("could not write the restore", err)
		return
	}

	// After the commit, as there: the task reads the row it is about.
	if _, err := r.Queue.Enqueue(ctx.Context(), ReindexOf(documentID.value,
		celeryqueue.LogContextFor(requested(ctx), requestID, caller.UserID))); err != nil {
		failed.uncaught("the restore was committed and its reindex could not be queued", err)
		return
	}

	body, err := pythonjson.Marshal(buildRestored(documentID.value, versionNumber.value))
	if err != nil {
		failed.uncaught(answerUnrendered, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// requested is the path the request named, which is where the Python
// middleware reads a task's log context from.
func requested(ctx huma.Context) string {
	address := ctx.URL()
	return address.Path
}

// ReindexOf is the message that has the restored text indexed: the
// indexing worker's own index_document (internal/indexqueue), which reads
// the row when it runs. The restore marks the row content_needs_reindexing,
// and the indexer then indexes the restored text instead of parsing the
// uploaded file over it. The Python tier queued reindex_document, whose body
// read workspace columns the user-keyed schema does not have.
func ReindexOf(documentID int64, context produce.LogContext) produce.Message {
	return indexqueue.Message(documentID, context)
}
