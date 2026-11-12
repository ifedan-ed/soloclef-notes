// Package agentflags answers which of the chat agent's optional features this
// deployment has, so the web app shows only controls that work:
//
//	GET /api/v1/agent/flags
//
// # What it does better than the Python
//
// The Python route answered twenty-four booleans, one per middleware of its
// agent stack, of which the web app read four. Most of those middlewares do
// not exist in this tier, and a flag that says "on" for something that is
// not there is a promise nobody keeps. This route answers the five a client
// reads, each from what this tier really does:
//
//   - disable_new_agent_stack and enable_permission are the deployment's own
//     settings, the same two that decide whether the permission-rule routes
//     answer (internal/agentrules), so the settings page and those routes
//     can never disagree.
//   - enable_action_log and enable_revert_route are true, and are constants
//     here, not settings: the chat's knowledge-base specialist records every
//     change it makes, in the change's own transaction, and the undo routes
//     take one back (internal/knowledgebase). Both are always there, so a
//     setting that switched either off would only hide a working undo. With
//     them the web app shows the undo on each change's card and under each
//     turn that made one.
//   - enable_desktop_local_filesystem is false: there is no desktop app.
package agentflags

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

// Path is the route's path.
const Path = "/api/v1/agent/flags"

// Served is the route as a "METHOD /path" pair.
func Served() []string { return []string{http.MethodGet + " " + Path} }

// Route is the route.
type Route struct {
	// Disabled is the deployment's kill-switch for the agent's optional
	// features (SOLOCLEF_DISABLE_NEW_AGENT_STACK).
	Disabled bool
	// PermissionEnabled says the permission rules are served
	// (SOLOCLEF_ENABLE_PERMISSION).
	PermissionEnabled bool
	Errors            errorenvelope.Writer
	Logger            *slog.Logger
}

// flags is the answer. The field names are the web app's contract
// (soloclef_web/lib/apis/agent-flags-api.service.ts).
type flags struct {
	Disabled        bool `json:"disable_new_agent_stack"`
	Permission      bool `json:"enable_permission"`
	ActionLog       bool `json:"enable_action_log"`
	RevertRoute     bool `json:"enable_revert_route"`
	LocalFilesystem bool `json:"enable_desktop_local_filesystem"`
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "agent-flags", Method: http.MethodGet, Path: Path,
		Summary: "Which optional agent features this deployment has", Tags: []string{"agent"},
	}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			response, requestID := humawriter.New(ctx), requestcontext.ID(ctx.Context())
			// The answer is the same for every account, but it describes
			// the deployment and is not for a stranger: the gate must have
			// named a caller.
			if _, ok := principal.FromContext(ctx.Context()); !ok {
				r.Logger.ErrorContext(ctx.Context(), "the agent flags route ran without a caller", slog.String("request_id", requestID))
				r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			// It changes only with a restart; a client may keep it a while.
			response.Header().Set("Cache-Control", "private, max-age=300")
			response.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(response).Encode(flags{Disabled: r.Disabled, Permission: r.PermissionEnabled, ActionLog: true, RevertRoute: true})
		}}, nil
	})
}
