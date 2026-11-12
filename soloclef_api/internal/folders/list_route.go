package folders

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// ListRoute serves the caller's whole folder tree as a flat list, which is
// the first thing the document browser asks for. It takes no parameter:
// whose folders is the caller, and the client builds the tree from each
// folder's `parent_id`.
type ListRoute struct {
	Folders Rows
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

func (r ListRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-folders",
		Method:      http.MethodGet,
		Path:        ListPath,
		Summary:     "The caller's folders, flat",
		Description: "Every folder of the caller's, ordered by position and then id.",
		Tags:        []string{"folders"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ListRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := failures{Errors: r.Errors, Logger: r.Logger, Message: "the folder list could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		fail.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	tree, err := r.Folders.Folders(ctx.Context(), caller.UserID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the folders", err)
		return
	}
	rendered := make([]any, 0, len(tree))
	for _, folder := range tree {
		one, err := Build(folder)
		if err != nil {
			fail.fail(ctx, response, requestID, "could not render a folder", err)
			return
		}
		rendered = append(rendered, one)
	}
	send(ctx, response, requestID, fail, rendered)
}
