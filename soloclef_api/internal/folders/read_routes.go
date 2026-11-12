package folders

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// ReadRoutes serves the two reads of one folder: the folder, and its
// ancestor chain.
type ReadRoutes struct {
	Folders Rows
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

// folderPath is the one path parameter the single-folder routes declare.
type folderPath struct {
	FolderID string `path:"folder_id" doc:"The folder's id"`
}

func (r ReadRoutes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-folder",
		Method:      http.MethodGet,
		Path:        ReadPath,
		Summary:     "One folder",
		Description: "One folder of the caller's.",
		Tags:        []string{"folders"},
	}, func(ctx context.Context, _ *folderPath) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.read}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "read-folder-breadcrumb",
		Method:      http.MethodGet,
		Path:        BreadcrumbPath,
		Summary:     "A folder's ancestors",
		Description: "The chain from the top level down to the folder itself.",
		Tags:        []string{"folders"},
	}, func(ctx context.Context, _ *folderPath) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.breadcrumb}, nil
	})
}

func (r ReadRoutes) read(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the folder could not be served"}

	_, folder, open := opening{Folders: r.Folders, Errors: r.Errors}.open(ctx, response, requestID, fail, nil)
	if !open {
		return
	}
	sendFolder(ctx, response, requestID, fail, *folder)
}

func (r ReadRoutes) breadcrumb(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the breadcrumb could not be served"}

	caller, folder, open := opening{Folders: r.Folders, Errors: r.Errors}.open(ctx, response, requestID, fail, nil)
	if !open {
		return
	}
	chain, err := r.Folders.Ancestors(ctx.Context(), caller.UserID, folder.ID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the folder's ancestors", err)
		return
	}
	send(ctx, response, requestID, fail, BuildBreadcrumb(chain))
}
