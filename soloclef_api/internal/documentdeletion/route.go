package documentdeletion

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// Access is the membership and workspace reader the gates need.
type Access interface {
	Membership(ctx context.Context, userID string, workspaceID int64) (*workspaceaccess.Membership, error)
	Workspace(ctx context.Context, workspaceID int64) (workspaceaccess.Workspace, error)
}

// Route serves the delete.
type Route struct {
	// Pool is taken rather than a store because the route runs two
	// transactions: the marker, and the revert for an enqueue that failed.
	Pool   *pgxpool.Pool
	Access Access
	Queue  celeryqueue.Enqueuer
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-document",
		Method:      http.MethodDelete,
		Path:        Path,
		Summary:     "Delete a document",
		Description: "The same response as the Python tier's " +
			"DELETE /api/v1/documents/{document_id}, byte for byte, the same " +
			"row marked, and the same Celery message on the same queue.",
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

	documentID, failure, rangeErr := requestnumber.Parse(
		ctx.Param("document_id"), "path", "document_id")
	if failure != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{*failure})
		return
	}
	if rangeErr != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return
	}

	refusal, err := r.mark(ctx.Context(), caller, documentID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not mark the document for deletion", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	// After the commit, as there: the task reads the row it is about, so a
	// message enqueued before the marker was durable could find the document
	// still "ready".
	if _, err := r.Queue.Enqueue(ctx.Context(), DeletionOf(documentID,
		celeryqueue.LogContextFor(ctx.URL().Path, requestID, caller.UserID))); err != nil {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError,
			"could not queue a document's deletion",
			slog.String("request_id", requestID),
			slog.Int64("document_id", documentID),
			slog.String("error", err.Error()))
		// The Python route's own compensation: a document left "deleting"
		// with nothing to delete it is hidden from search forever.
		if _, revertErr := database.Write(ctx.Context(), r.Pool,
			func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
				return struct{}{}, New(tx).MarkDocumentReady(ctx, documentID)
			}); revertErr != nil {
			r.fail(ctx, response, requestID, "could not restore a document whose deletion was not queued", revertErr)
			return
		}
		r.Errors.Write(response, http.StatusServiceUnavailable, QueueFailed, requestID)
		return
	}

	body, err := pythonjson.Marshal(Message())
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the answer", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// mark is the refusals and then the marker.
//
// The read and the gates are on the pool and only the write is in a
// transaction, which is the shape every write here has
// (internal/workspaceroles): the Python route holds one session across all of
// it, but with one statement to write there is nothing for a transaction to
// make atomic that a single statement does not. It does not serialise two
// deletes of the same document — both would read "ready" and both would
// enqueue — and neither does the Python route, which takes no lock either.
// The duplicate is harmless there and here: the task is idempotent and the
// second message finds the document already gone.
func (r Route) mark(ctx context.Context, caller principal.Principal, documentID int64) (*workspaceaccess.Refusal, error) {
	document, err := New(r.Pool).DocumentForDeletion(ctx, documentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return &workspaceaccess.Refusal{
			Status:  http.StatusNotFound,
			Message: NotFound(documentID),
		}, nil
	}
	if err != nil {
		return nil, err
	}

	// The state before the permission, as there: a document being processed is
	// refused whoever asked.
	state, err := StateOf(document.Status)
	if err != nil {
		return nil, err
	}
	if message := RefusalForState(state); message != "" {
		return &workspaceaccess.Refusal{Status: http.StatusConflict, Message: message}, nil
	}

	membership, err := r.Access.Membership(ctx, caller.UserID, document.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if refusal := workspaceaccess.CheckMembership(membership, DocumentsDelete, DeleteDenied); refusal != nil {
		return refusal, nil
	}
	workspace, err := r.Access.Workspace(ctx, document.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if refusal := workspaceaccess.CheckAPIAccess(workspace, caller.IsGated()); refusal != nil {
		return refusal, nil
	}

	_, err = database.Write(ctx, r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
			return struct{}{}, New(tx).MarkDocumentDeleting(ctx, documentID)
		})
	return nil, err
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
