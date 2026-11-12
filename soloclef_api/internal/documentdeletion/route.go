package documentdeletion

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// Owners says whether an account has a document.
type Owners interface {
	Owns(ctx context.Context, documentID int64, userID string) (bool, error)
}

// Remover removes a document (Removal).
type Remover interface {
	Remove(ctx context.Context, documentID int64) error
}

// removalTimeout bounds one removal. It runs outside the request's context
// (see serve), so it needs a limit of its own; a document of a hundred
// thousand chunks is a hundred rounds of a few tens of milliseconds.
const removalTimeout = 2 * time.Minute

// Route serves the delete.
type Route struct {
	Owners  Owners
	Removal Remover
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-document",
		Method:      http.MethodDelete,
		Path:        Path,
		Summary:     "Delete a document",
		Description: "Removes the document's vectors, chunks, stored files and row. " +
			"Answers once the document is gone; safe to repeat.",
		Tags: []string{"documents"},
	}, func(ctx context.Context, _ *struct {
		DocumentID string `path:"document_id" doc:"The document's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// delete if the wiring is ever wrong.
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	documentID, failure, rangeErr := requestnumber.Parse(ctx.Param("document_id"), "path", "document_id")
	if failure != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{*failure})
		return
	}
	// A number no document can have names no document; asking the database
	// would be an error instead of a "not found".
	if rangeErr != nil || documentID < 1 {
		r.Errors.Write(response, http.StatusNotFound, NotFound(documentID), requestID)
		return
	}

	// The document's state is not asked about. A pending or processing
	// document can be deleted (the indexing worker checks the marker before
	// it writes), and one already marked "deleting" is exactly the case a
	// repeated delete exists for: an earlier attempt stopped partway, and
	// this one finishes it.
	owned, err := r.Owners.Owns(ctx.Context(), documentID, caller.UserID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read whose document this is", err)
		return
	}
	if !owned {
		// Another account's document and an id that names nothing are the
		// same answer, so document ids cannot be probed for.
		r.Errors.Write(response, http.StatusNotFound, NotFound(documentID), requestID)
		return
	}

	// Not the request's context. A delete that has begun should finish even
	// if the person closes the tab: a document half removed is repaired by
	// asking again, but nobody asks again for a request they walked away
	// from.
	removing, done := context.WithTimeout(context.WithoutCancel(ctx.Context()), removalTimeout)
	defer done()
	started := time.Now()
	if err := r.Removal.Remove(removing, documentID); err != nil {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "a document was not fully deleted; it is left marked and the delete can be repeated",
			slog.String("request_id", requestID), slog.Int64("document_id", documentID),
			slog.Duration("after", time.Since(started)), slog.String("error", err.Error()))
		r.Errors.Write(response, http.StatusServiceUnavailable, Unfinished, requestID)
		return
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "document deleted",
		slog.String("request_id", requestID), slog.Int64("document_id", documentID),
		slog.Duration("took", time.Since(started)))

	body, err := pythonjson.Marshal(Message())
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the answer", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r Route) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the document could not be deleted", attributes...)
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
