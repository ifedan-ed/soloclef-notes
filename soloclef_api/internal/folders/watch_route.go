package folders

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// WatchRoute serves `PATCH /api/v1/folders/{folder_id}/watched`: stop
// keeping a folder in step with a folder on someone's computer.
//
// It takes no body. The statement decides whether anything is written: a
// folder that is watched has its flag turned off, and any other folder is
// left exactly as it was. The answer is the same either way, because either
// way the folder is not watched afterwards.
type WatchRoute struct {
	Folders Rows
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
		Description: "Turns off the `watched` flag in the metadata of a folder of the caller's.",
		Tags:        []string{"folders"},
	}, func(ctx context.Context, _ *folderPath) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r WatchRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the folder's watch status could not be updated"}

	caller, folder, open := opening{Folders: r.Folders, Errors: r.Errors}.open(ctx, response, requestID, fail, nil)
	if !open {
		return
	}
	if err := r.Folders.ClearWatched(ctx.Context(), caller.UserID, folder.ID, nowOr(r.Now)); err != nil {
		fail.fail(ctx, response, requestID, "could not write the folder's metadata", err)
		return
	}
	sendMessage(ctx, response, requestID, fail, WatchUpdated)
}
