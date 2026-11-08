package folders

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// ReadRoutes serves the two reads: one folder, and one folder's ancestor
// chain. They are one type because they share the gate, the renderer and the
// 404. The workspace's flat list is not here (see the package comment).
type ReadRoutes struct {
	Folders Store
	Access  Access
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

func (r ReadRoutes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-folder",
		Method:      http.MethodGet,
		Path:        ReadPath,
		Summary:     "One folder",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/folders/{folder_id}, byte for byte.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *struct {
		FolderID string `path:"folder_id" doc:"The folder's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.read}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "read-folder-breadcrumb",
		Method:      http.MethodGet,
		Path:        BreadcrumbPath,
		Summary:     "A folder's ancestors",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/folders/{folder_id}/breadcrumb, byte for byte.",
		Tags: []string{"folders"},
	}, func(ctx context.Context, _ *struct {
		FolderID string `path:"folder_id" doc:"The folder's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.breadcrumb}, nil
	})
}

func (r ReadRoutes) failures(message string) failures {
	return failures{Errors: r.Errors, Logger: r.Logger, Message: message}
}

func (r ReadRoutes) read(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := r.failures("the folder could not be served")

	gate := folderGate{Folders: r.Folders, Access: r.Access, Errors: r.Errors}
	_, folder, open := gate.open(ctx, response, requestID, fail, nil, DocumentsRead, ReadDenied)
	if !open {
		return
	}

	rendered, err := Build(*folder)
	if err != nil {
		fail.failWhileRendering(ctx, response, requestID, "could not render the folder", err)
		return
	}
	body, err := marshal(rendered)
	if err != nil {
		fail.failWhileRendering(ctx, response, requestID, "could not render the folder", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r ReadRoutes) breadcrumb(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := r.failures("the breadcrumb could not be served")

	gate := folderGate{Folders: r.Folders, Access: r.Access, Errors: r.Errors}
	_, folder, open := gate.open(ctx, response, requestID, fail, nil, DocumentsRead, ReadDenied)
	if !open {
		return
	}

	chain, err := r.Folders.Ancestors(ctx.Context(), folder.ID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the folder's ancestors", err)
		return
	}
	body, err := marshal(BuildBreadcrumb(chain))
	if err != nil {
		fail.failWhileRendering(ctx, response, requestID, "could not render the breadcrumb", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
