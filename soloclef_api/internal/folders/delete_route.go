package folders

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// DeleteRoute serves the delete, which itself deletes no folder.
//
// It marks every document in the subtree as being deleted, which takes them
// out of search at once, and queues the job that removes the documents and
// then the folders. The marker is stored before the job is queued, because
// the job reads the rows it is about.
//
// If the job cannot be queued the marker is undone, each document getting
// back the status it had, and the answer is 503: documents left marked with
// nothing coming to delete them would be hidden for good.
type DeleteRoute struct {
	Folders Rows
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
		Description: "Marks the documents in a folder of the caller's, and in every folder under it, " +
			"as being deleted, and queues their removal and the folders'.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *folderPath) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r DeleteRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the folder could not be deleted"}

	caller, folder, open := opening{Folders: r.Folders, Errors: r.Errors}.open(ctx, response, requestID, fail, nil)
	if !open {
		return
	}

	marked, err := r.Folders.MarkSubtreeDeleting(ctx.Context(), caller.UserID, folder.ID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not mark the folder's documents for deletion", err)
		return
	}
	if len(marked.SubtreeIDs) == 0 {
		// Deleted by another request between the read and the marker.
		r.Errors.Write(response, http.StatusNotFound, NotFound, requestID)
		return
	}

	documentIDs := marked.DocumentIDs()
	if _, err := r.Queue.Enqueue(ctx.Context(), DeletionOf(caller.UserID, documentIDs, marked.SubtreeIDs,
		celeryqueue.LogContextFor(ctx.URL().Path, requestID, caller.UserID))); err != nil {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError,
			"could not queue a folder's deletion",
			slog.String("request_id", requestID),
			slog.Int64("folder_id", folder.ID),
			slog.String("error", err.Error()))
		// Not the request's context: a client that has gone away must not
		// leave the documents marked.
		if revertErr := r.Folders.RestoreMarked(context.WithoutCancel(ctx.Context()), caller.UserID, marked); revertErr != nil {
			fail.fail(ctx, response, requestID,
				"could not restore documents whose deletion was not queued", revertErr)
			return
		}
		r.Errors.Write(response, http.StatusServiceUnavailable, QueueFailed, requestID)
		return
	}
	send(ctx, response, requestID, fail, DeletionAnswer(len(documentIDs)))
}
