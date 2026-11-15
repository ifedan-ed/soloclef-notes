package prompts

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

// Prompts reads the stored prompts.
type Prompts interface {
	Prompts(ctx context.Context, callerID string) ([]Prompt, error)
}

// ListRoute serves GET /api/v1/prompts (list_prompts): the caller's own
// library, which is also what the chat composer's "/" menu offers.
//
// There is deliberately no GET of one prompt: the settings page and the
// composer both work from the list.
type ListRoute struct {
	Prompts Prompts
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

func (r ListRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-prompts",
		Method:      http.MethodGet,
		Path:        ListPath,
		Summary:     "The caller's own prompts",
		Description: "Every prompt of the signed-in account, newest first.",
		Tags:        []string{"prompts"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ListRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, ok := requireSession(ctx, r.Errors, r.Logger, response, requestID, listUnserved)
	if !ok {
		return
	}

	stored, err := r.Prompts.Prompts(ctx.Context(), caller.UserID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's prompts", err)
		return
	}
	body, err := pythonjson.Marshal(BuildList(stored))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the prompt list", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r ListRoute) fail(ctx huma.Context, response http.ResponseWriter,
	requestID, reason string, cause error) {
	writeFailure(ctx, r.Errors, r.Logger, response, requestID, reason, cause, listUnserved)
}
