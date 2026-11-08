package folders

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// DeleteRoute serves the delete, which deletes no folder.
//
// It marks every document in the subtree for deletion, commits, and enqueues
// the task that removes the documents and then the folders. If the enqueue
// fails it puts the documents back to "ready" and answers 503 — not an
// after-commit effect, because an effect cannot fail the request and this must
// (internal/database).
type DeleteRoute struct {
	// Pool is taken rather than only a store because the route runs two
	// transactions: the marker, and the revert for an enqueue that failed.
	Pool    *pgxpool.Pool
	Folders Store
	Access  Access
	Queue   celeryqueue.Enqueuer
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

func (r DeleteRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-folder",
		Method:      http.MethodDelete,
		Path:        DeletePath,
		Summary:     "Delete a folder and its documents",
		Description: "The same response as the Python tier's " +
			"DELETE /api/v1/folders/{folder_id}, byte for byte, the same rows " +
			"marked, and the same Celery message on the same queue.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *struct {
		FolderID string `path:"folder_id" doc:"The folder's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r DeleteRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the folder could not be deleted"}

	gate := folderGate{Folders: r.Folders, Access: r.Access, Errors: r.Errors}
	caller, folder, open := gate.open(ctx, response, requestID, fail, nil,
		DocumentsDelete, DeleteDenied)
	if !open {
		return
	}

	// The Python route reads the folder's git path here, before the subtree,
	// and passes it to record_removed_folder after the commit. That read has
	// no effect of its own — it builds a path index from rows and returns a
	// string — and the verb it feeds is a no-op while the knowledge store is
	// off, which StoreDelegate is what guarantees. So it is not mirrored.
	subtreeIDs, err := r.Folders.SubtreeIDs(ctx.Context(), folder.ID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the folder's subtree", err)
		return
	}
	documentIDs, err := r.Folders.DocumentsToDelete(ctx.Context(), subtreeIDs)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the folder's documents", err)
		return
	}

	// The marker is committed before the enqueue, because the task reads the
	// rows it is about. The Python route commits only when there is something
	// to mark, which is why an empty list runs no statement here either.
	if len(documentIDs) > 0 {
		if _, err := database.Write(ctx.Context(), r.Pool,
			func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
				return struct{}{}, MarkDeleting(ctx, tx, documentIDs)
			}); err != nil {
			fail.fail(ctx, response, requestID, "could not mark the folder's documents for deletion", err)
			return
		}
	}

	if _, err := r.Queue.Enqueue(ctx.Context(), DeletionOf(documentIDs, subtreeIDs,
		celeryqueue.LogContextFor(ctx.URL().Path, requestID, caller.UserID))); err != nil {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError,
			"could not queue a folder's deletion",
			slog.String("request_id", requestID),
			slog.Int64("folder_id", folder.ID),
			slog.String("error", err.Error()))
		// The Python route's own compensation: documents left "deleting" with
		// nothing to delete them are hidden from search forever.
		if len(documentIDs) > 0 {
			if _, revertErr := database.Write(ctx.Context(), r.Pool,
				func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
					return struct{}{}, MarkReady(ctx, tx, documentIDs)
				}); revertErr != nil {
				fail.fail(ctx, response, requestID,
					"could not restore documents whose deletion was not queued", revertErr)
				return
			}
		}
		r.Errors.Write(response, http.StatusServiceUnavailable, QueueFailed, requestID)
		return
	}

	body, err := pythonjson.Marshal(DeletionAnswer(len(documentIDs)))
	if err != nil {
		fail.failWhileRendering(ctx, response, requestID, "could not render the answer", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
