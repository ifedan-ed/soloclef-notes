package modelconnections

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// ProvidersRoute serves GET /api/v1/model-providers: the providers a
// connection can be made to, and what each one needs.
//
// It reads no table and no setting; the whole answer is
// app/services/provider_registry.py's REGISTRY (providers.go). It is behind
// `require_session_context`, so an API key and an agent are refused in that
// tier's own words, and it takes no parameter, so nothing in a query string
// can change the answer or turn it into a 422.
type ProvidersRoute struct {
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ProvidersRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-model-providers",
		Method:      http.MethodGet,
		Path:        ProvidersPath,
		Summary:     "The providers a model connection can be made to",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/model-providers, byte for byte.",
		Tags: []string{"model connections"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ProvidersRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := func(reason string, cause error) {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"the provider list could not be served", reason, cause)
	}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an answer to nobody
		// if the wiring is ever wrong.
		failed(noCaller, nil)
		return
	}
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return
	}

	body, err := pythonjson.Marshal(BuildProviders())
	if err != nil {
		// Unreachable: the list is a constant of strings and booleans.
		failed("could not render the provider list", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
