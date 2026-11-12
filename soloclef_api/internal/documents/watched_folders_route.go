package documents

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// WatchedFolders reads what the watched-folder list reads.
type WatchedFolders interface {
	// WatchedFoldersOf is the account's root folders marked as watched, in
	// id order.
	WatchedFoldersOf(ctx context.Context, userID string) ([]folders.Folder, error)
}

// WatchedFoldersRoute serves GET /documents/watched-folders
// (get_watched_folders): the caller's root folders that a desktop
// client is keeping in step with a directory, as list[FolderRead].
//
// A folder is watched when its metadata says `"watched": true`. The
// statement compares the value as text, so the string "true" counts as well;
// both count.
//
// # The order, which is why this route could not be ported before
//
// The statement had no ORDER BY, and a watched folder's row is rewritten on
// every sync, which moves it in the table. Since 2026-11-11 the Python
// statement ends `ORDER BY folders.id` and this one does too.
//
// # It catches nothing
//
// Unlike the other two reads beside it, this route has no try. Whatever goes
// wrong is answered by Starlette's outermost handler, so its 500 carries no
// CORS headers (failure.go).
//
// The folder is rendered by internal/folders.Build, which is FolderRead for
// every other route that answers one: one rendering of one model.
type WatchedFoldersRoute struct {
	Folders WatchedFolders
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

func (r WatchedFoldersRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-watched-folders",
		Method:      http.MethodGet,
		Path:        WatchedFoldersPath,
		Summary:     "The caller's watched root folders",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/documents/watched-folders, byte for byte.",
		Tags: []string{"documents"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r WatchedFoldersRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, what: "the watched folders could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.uncaught(noCaller, nil)
		return
	}

	watched, err := r.Folders.WatchedFoldersOf(ctx.Context(), caller.UserID)
	if err != nil {
		failed.uncaught("could not read the caller's watched folders", err)
		return
	}

	rendered := make([]any, 0, len(watched))
	for _, folder := range watched {
		one, err := folders.Build(folder)
		if err != nil {
			failed.uncaught("could not render a watched folder", err)
			return
		}
		rendered = append(rendered, one)
	}
	body, err := pythonjson.Marshal(rendered)
	if err != nil {
		failed.uncaught(answerUnrendered, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
