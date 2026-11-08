package folders

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// WatchRoute serves `PATCH /api/v1/folders/{folder_id}/watched`, which clears
// the watched flag from a folder's metadata.
//
// It is the smallest route in the slice and the one with the most in it, for
// three reasons that are all in watchedCleared:
//
//   - it takes no body, has no response_model, and so is rendered by
//     Starlette's own json.dumps rather than by pydantic;
//   - it is the one route of the nine whose Python counterpart has **no
//     try/except**, so every 500 of its own is answered from outside the CORS
//     middleware and carries no CORS headers (internal/corsmirror.Suppress);
//   - it writes nothing at all for most rows, and what decides that is Python
//     dict equality rather than anything a response shows.
type WatchRoute struct {
	Pool    *pgxpool.Pool
	Folders Store
	Access  Access
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	Now     func() time.Time
}

func (r WatchRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "stop-watching-folder",
		Method:      http.MethodPatch,
		Path:        WatchedPath,
		Summary:     "Stop watching a folder",
		Description: "The same response as the Python tier's " +
			"PATCH /api/v1/folders/{folder_id}/watched, byte for byte, and the same row.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *struct {
		FolderID string `path:"folder_id" doc:"The folder's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r WatchRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	// The one route here whose Python counterpart catches nothing, so its own
	// 500s carry no CORS headers.
	fail := failures{
		Errors:          r.Errors,
		Logger:          r.Logger,
		Message:         "the folder's watch status could not be updated",
		NoCORSOnFailure: true,
	}

	gate := folderGate{Folders: r.Folders, Access: r.Access, Errors: r.Errors}
	_, folder, open := gate.open(ctx, response, requestID, fail, nil,
		DocumentsUpdate, UpdateDenied)
	if !open {
		return
	}

	cleared, write, err := watchedCleared(folder.Metadata)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the folder's metadata", err)
		return
	}
	if write {
		at := nowOr(r.Now)
		if _, err := database.Write(ctx.Context(), r.Pool,
			func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
				return struct{}{}, SetMetadata(ctx, tx, folder.ID, cleared, at)
			}); err != nil {
			fail.fail(ctx, response, requestID, "could not write the folder's metadata", err)
			return
		}
	}

	answer := pythonjson.NewObject()
	answer.Set("message", WatchUpdated)
	body, err := pythonjson.Marshal(answer)
	if err != nil {
		fail.failWhileRendering(ctx, response, requestID, "could not render the answer", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
