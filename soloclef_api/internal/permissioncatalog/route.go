package permissioncatalog

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Path is the route this tier takes over from the Python tier
// (custom/docs/go-api-tier.md).
const Path = "/api/v1/permissions"

// Route serves the catalog. It has no store and no gate beyond being
// authenticated: the Python route takes no workspace and reads no row, so there
// is nothing for the RBAC gates to decide.
type Route struct {
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-permission-catalog",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "Everything a role can be given",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/permissions, byte for byte.",
		Tags: []string{"roles"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	if _, authenticated := principal.FromContext(ctx.Context()); !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// read if the wiring is ever wrong.
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError,
			"the permission catalogue could not be served",
			slog.String("request_id", requestID),
			slog.String("reason", "the route ran without an authenticated caller"))
		corsmirror.Suppress(ctx.Context())
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}

	body, err := pythonjson.Marshal(Listing())
	if err != nil {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError,
			"the permission catalogue could not be served",
			slog.String("request_id", requestID),
			slog.String("reason", "could not render the catalogue"),
			slog.String("error", err.Error()))
		// list_all_permissions catches nothing, so this is a 500 Starlette's
		// outermost handler writes — outside CORSMiddleware, and so without
		// the CORS headers (internal/corsmirror.Suppress).
		corsmirror.Suppress(ctx.Context())
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
