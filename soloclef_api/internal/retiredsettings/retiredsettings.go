// Package retiredsettings answers the one route of a removed Settings
// feature that the installed phone build still calls:
//
//	GET /api/v1/global-model-connections  200 []
//
// # Why it is here
//
// iPhone 1.1.0 from the App Store (internal/installedbuilds) opens Admin,
// Model connections, which lists the operator's connections beside the
// account's own and shows only "Couldn't load" when the operator's list is a
// 404: the account's own connections are hidden with it, so nobody could see
// or switch off their own keys. Operator connections are not offered any
// more (removed on 2026-11-13), so an empty list is the truth, and the page
// works around it.
//
// Nothing is read or written: the answer is the same for every caller, so
// it says nothing about anyone. The route stands behind the gate like every
// route of an account.
//
// It goes when no installed bundle calls it (compat.go of
// internal/installedbuilds lists it with the bundles that need it).
// installed-builds: retired-operator-connections
package retiredsettings

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

// OperatorConnectionsPath is the path, as the installed phone build calls it.
const OperatorConnectionsPath = "/api/v1/global-model-connections"

// Served is the route as a "METHOD /path" pair, for the gate's list.
func Served() []string {
	return []string{http.MethodGet + " " + OperatorConnectionsPath}
}

// Routes is the route.
type Routes struct {
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// noConnections is the answer: the list the build parses, with nothing in it.
var noConnections = []byte(`[]`)

// Register mounts it.
func (r Routes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "retired-operator-connections", Method: http.MethodGet, Path: OperatorConnectionsPath,
		Summary: "Operator model connections (none: for installed phone builds)", Tags: []string{"installed builds"},
	}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			response, requestID := humawriter.New(ctx), requestcontext.ID(ctx.Context())
			// The gate names a caller before this runs; one without is a
			// wiring mistake, not a stranger.
			if _, ok := principal.FromContext(ctx.Context()); !ok {
				r.Logger.ErrorContext(ctx.Context(), "the retired operator connections route ran without a caller", slog.String("request_id", requestID))
				r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("Cache-Control", "private, no-store")
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write(noConnections)
		}}, nil
	})
}
