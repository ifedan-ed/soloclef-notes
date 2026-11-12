package agentsignin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// SetupPath is GET /api/v1/agent-sign-in: what "Connect your agent" shows
// for claude.ai and ChatGPT (the web's components/mcp/sign-in-setup.tsx).
const SetupPath = "/api/v1/agent-sign-in"

// SetupServed is the route as a "METHOD /path" pair.
func SetupServed() []string { return []string{http.MethodGet + " " + SetupPath} }

// SetupRoute answers the client id and client secret a person pastes into
// claude.ai's or ChatGPT's custom connector form. Both need them pasted
// because the identity provider offers those two no registration they can
// use themselves.
//
// The secret is shown to every signed-in account, on purpose: every account
// may connect its own Claude or ChatGPT, and the secret lets nobody in by
// itself. The provider only ever returns a sign-in to those two products'
// own callbacks, after the person's own login.
//
// What keeps it from going further than that:
//
//   - A browser session only. An API key or an agent's own token has no
//     settings page to show it on, and a leaked key must not be a way to
//     read a second credential.
//   - Never stored by a browser or a proxy: the answer is marked no-store,
//     which the Python tier did not do.
type SetupRoute struct {
	// Settings is the same value the token verifier is built from, so the
	// client id shown is by construction the audience a token is held to.
	Settings Settings
	// ClientSecret is the agent application's secret at the provider, or
	// empty for a public client (one registered with no secret).
	ClientSecret string
	Errors       errorenvelope.Writer
	Logger       *slog.Logger
}

// setupDetails is the answer, in the shape the web reads
// (lib/apis/agent-sign-in-api.service.ts): both values are null, not empty,
// when there is nothing to paste.
type setupDetails struct {
	Enabled      bool    `json:"enabled"`
	ClientID     *string `json:"client_id"`
	ClientSecret *string `json:"client_secret"`
}

// Register mounts the route.
func (r SetupRoute) Register(api huma.API) {
	// The answer is the same for every account and changes only with a
	// restart, so it is encoded once.
	details := setupDetails{}
	if r.Settings.Enabled() {
		details.Enabled = true
		details.ClientID = &r.Settings.ClientID
		if r.ClientSecret != "" {
			details.ClientSecret = &r.ClientSecret
		}
	}
	body, _ := json.Marshal(details)

	huma.Register(api, huma.Operation{
		OperationID: "agent-sign-in-setup", Method: http.MethodGet, Path: SetupPath,
		Summary: "What an AI agent's sign-in form needs pasted", Tags: []string{"agent-sign-in"},
	}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			response := humawriter.New(ctx)
			requestID := requestcontext.ID(ctx.Context())
			caller, ok := principal.FromContext(ctx.Context())
			if !ok {
				// The gate did not run: a wiring fault, never the caller's.
				r.Logger.ErrorContext(ctx.Context(), "the agent sign-in setup route ran without a caller", slog.String("request_id", requestID))
				r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
				return
			}
			if caller.Method != principal.MethodSession {
				r.Errors.Write(response, http.StatusForbidden, "The agent sign-in details are shown to a signed-in browser only.", requestID)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("Cache-Control", "no-store")
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write(body)
		}}, nil
	})
}
