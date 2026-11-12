package modelchoices

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// SetupRoute serves GET /api/v1/users/me/llm-setup-status: can this account
// chat, and if not, what kind of "not".
//
// The answer is derived on every read and never stored. The Python route
// stamped a timestamp on a GET to remember that an account had once been
// set up; the same fact is in the row here: an account with a chat choice
// that is no longer offered is recovering, and one with no choice whose
// deployment default is not offered was never set up.
//
//	status         "ready" when the model that would answer is on the proxy
//	source         "models" for the account's own choice, "global_config"
//	               for the deployment's default, "none" when not ready
//	stage          "ready", "recovery" (its choice was withdrawn) or
//	               "initial_setup" (the deployment's default is not offered)
//	can_configure  whether this caller may fix it: choosing a model is the
//	               settings screen's, so a session may and a key may not
type SetupRoute struct {
	Choices Choices
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

type setupStatus struct {
	Status       string `json:"status"`
	Source       string `json:"source"`
	CanConfigure bool   `json:"can_configure"`
	Stage        string `json:"stage"`
}

// Register adds the operation.
func (r SetupRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-llm-setup-status", Method: http.MethodGet, Path: SetupStatusPath,
		Summary: "Whether the caller has a chat model it can use", Tags: []string{"model choices"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r SetupRoute) serve(ctx huma.Context) {
	request := begin(ctx, r.Errors, r.Logger, "an account's model setup status could not be read")
	caller, ok := request.caller(false)
	if !ok {
		return
	}
	looked, err := r.Choices.Look(ctx.Context(), caller.UserID, Chat)
	if err != nil {
		request.refuse("could not read the chat model", err)
		return
	}
	request.send(statusOf(looked[Chat], caller.IsSession()))
}

func statusOf(chat Chosen, session bool) setupStatus {
	status := setupStatus{CanConfigure: session}
	switch {
	case chat.Offered && chat.Selected != "":
		status.Status, status.Source, status.Stage = "ready", "models", "ready"
	case chat.Offered:
		status.Status, status.Source, status.Stage = "ready", "global_config", "ready"
	case chat.Selected != "":
		status.Status, status.Source, status.Stage = "needs_setup", "none", "recovery"
	default:
		status.Status, status.Source, status.Stage = "needs_setup", "none", "initial_setup"
	}
	return status
}
