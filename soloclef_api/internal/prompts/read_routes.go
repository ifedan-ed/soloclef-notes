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
	PublicPrompts(ctx context.Context, callerID string) ([]Prompt, error)
}

// ReadRoutes serves the two lists: the caller's own library, and the community
// shelf.
//
// They are one type because they are one screen and one gate, the way the role
// editor's two reads are — and because the pair is where the route-ordering
// question lives. `GET /api/v1/prompts/public` is a literal that shares its
// prefix with `PUT`/`DELETE /api/v1/prompts/{prompt_id}`, which the Python
// router declares **first**; Starlette still reaches the literal, because its
// router remembers a path-matched-but-method-mismatched route as a *partial*
// match and keeps looking for a full one. Asked of the real router:
// `GET /api/v1/prompts/public` is list_public_prompts and not a prompt_id of
// "public". There is no `GET /api/v1/prompts/{prompt_id}` at all, so Go's
// ServeMux has nothing to disagree with — and `PUT /api/v1/prompts/public` is
// the path parameter's own 422 on both tiers, which is the case that would
// have shown a disagreement.
//
// What the two routers do **not** agree on is a path neither route answers:
// `GET /api/v1/prompts/7` is Starlette's 405 `{"detail":"Method Not
// Allowed"}` with an `allow` header, and this tier's bare 404. That is why the
// proxy's GET matcher for this slice names the two literals and no `[^/]+`
// (custom/docs/go-api-tier.md).
type ReadRoutes struct {
	Prompts Prompts
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

func (r ReadRoutes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-prompts",
		Method:      http.MethodGet,
		Path:        ListPath,
		Summary:     "The caller's own prompts",
		Description: "The same response as the Python tier's GET /api/v1/prompts, " +
			"byte for byte.",
		Tags: []string{"prompts"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveList}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "read-public-prompts",
		Method:      http.MethodGet,
		Path:        PublicPath,
		Summary:     "The community's published prompts",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/prompts/public, byte for byte.",
		Tags: []string{"prompts"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.servePublic}, nil
	})
}

func (r ReadRoutes) serveList(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, ok := requireSession(ctx, r.Errors, r.Logger, response, requestID, listUnserved)
	if !ok {
		return
	}

	stored, err := r.Prompts.Prompts(ctx.Context(), caller.UserID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's prompts", err, listUnserved)
		return
	}
	body, err := pythonjson.Marshal(BuildList(stored))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the prompt list", err, listUnserved)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r ReadRoutes) servePublic(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, ok := requireSession(ctx, r.Errors, r.Logger, response, requestID, shelfUnserved)
	if !ok {
		return
	}
	stored, err := r.Prompts.PublicPrompts(ctx.Context(), caller.UserID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the community prompts", err, shelfUnserved)
		return
	}
	body, err := pythonjson.Marshal(BuildPublicList(stored))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the community prompt list", err, shelfUnserved)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r ReadRoutes) fail(ctx huma.Context, response http.ResponseWriter,
	requestID, reason string, cause error, message string) {
	writeFailure(ctx, r.Errors, r.Logger, response, requestID, reason, cause, message)
}
