package modelchoices

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// answer is one job's model as the apps read it: the shape of
// /users/me/chat-model's answer, so one picker component draws them all.
type answer struct {
	// Selected is the account's own choice, null when it has made none.
	Selected *string `json:"selected"`
	// Model is what does the job now and Available whether the proxy still
	// offers it. Model is null when the account chose nothing and the
	// deployment names no model for the job.
	Model     *string `json:"model"`
	Available bool    `json:"available"`
	// Default is what does the job for an account that chose none.
	Default *string `json:"default"`
	// Models is the menu, each entry saying whether it is the account's
	// own model (ownmodels.MenuEntry, as the chat's menu answers it).
	Models []ownmodels.MenuEntry `json:"models"`
}

func answerOf(chosen Chosen) answer {
	out := answer{Available: chosen.Offered, Models: ownmodels.MenuEntries(chosen.Models, chosen.Own)}
	out.Selected, out.Model, out.Default = named(chosen.Selected), named(chosen.Model), named(chosen.Default)
	return out
}

// named is a name, or JSON null for none.
func named(name string) *string {
	if name == "" {
		return nil
	}
	return &name
}

const (
	proxyUnreachable = "The model service cannot be reached right now. Try again in a moment."
	sessionRequired  = "This action requires an interactive session"
)

// request is what every route here starts with: the response, the request
// id, and how a failure is answered and logged.
type request struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	errors    errorenvelope.Writer
	logger    *slog.Logger
	// what names the route in a log line.
	what string
}

func begin(ctx huma.Context, errors errorenvelope.Writer, logger *slog.Logger, what string) request {
	return request{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), errors: errors, logger: logger, what: what}
}

// caller is who asked. writes says the route changes a setting: that is the
// settings screen's to do, so an API key or an agent is refused, while a
// read is answered to any caller the gate let through.
func (r request) caller(writes bool) (principal.Principal, bool) {
	caller, authenticated := principal.FromContext(r.ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an answer to
		// nobody if the wiring is ever wrong.
		r.fail("the route ran without an authenticated caller", nil)
		return caller, false
	}
	if writes && !caller.IsSession() {
		r.errors.Write(r.response, http.StatusForbidden, sessionRequired, r.requestID)
		return caller, false
	}
	return caller, true
}

// fail is the tier's 500. The log line carries the reason and the error.
func (r request) fail(reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", r.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.logger.LogAttrs(r.ctx.Context(), slog.LevelError, r.what, attributes...)
	r.errors.Write(r.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, r.requestID)
}

// refuse answers a failed Look: the proxy being down is a 503 in words a
// person can act on, anything else the 500.
func (r request) refuse(reason string, err error) {
	if errors.Is(err, ErrProxyUnreachable) {
		r.logger.LogAttrs(r.ctx.Context(), slog.LevelError, r.what,
			slog.String("request_id", r.requestID), slog.String("reason", reason), slog.String("error", err.Error()))
		r.errors.Write(r.response, http.StatusServiceUnavailable, proxyUnreachable, r.requestID)
		return
	}
	r.fail(reason, err)
}

func (r request) invalid(field, message string) {
	r.errors.WriteValidation(r.response, r.requestID, []errorenvelope.Failure{{Location: []string{"body", field}, Message: message}})
}

func (r request) send(value any) {
	body, err := json.Marshal(value)
	if err != nil {
		r.fail("could not render the answer", err)
		return
	}
	r.response.Header().Set("Content-Type", "application/json")
	r.response.WriteHeader(http.StatusOK)
	_, _ = r.response.Write(body)
}

// readName reads one field of a body that names a model: a name, or null
// for the default. cleared says it was null.
//
// A field that is present and unreadable is told apart from one that is
// absent, because the two mean different things to the routes: absent is
// "leave this job alone" (or, where the field is required, a mistake), and
// neither may be mistaken for the null that clears a choice.
func readName(raw json.RawMessage) (name string, cleared, ok bool) {
	if string(raw) == "null" {
		return "", true, true
	}
	if err := json.Unmarshal(raw, &name); err != nil || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > maxModelNameChars {
		return "", false, false
	}
	return name, false, true
}
