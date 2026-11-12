package knowledgebase

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// The undo routes. `{thread_id}` is the name every route under
// /api/v1/threads/ gives that segment, which a ServeMux requires of
// patterns that share it.
const (
	RevertPath     = "/api/v1/threads/{thread_id}/revert/{action_id}"
	RevertTurnPath = "/api/v1/threads/{thread_id}/revert-turn/{chat_turn_id}"
)

// Served is the two routes as "METHOD /path" pairs.
func Served() []string {
	return []string{http.MethodPost + " " + RevertPath, http.MethodPost + " " + RevertTurnPath}
}

const (
	threadAbsent = "Thread not found"
	actionAbsent = "Action not found"
	// maxTurnIDChars is the column's width (agent_action_log.chat_turn_id).
	maxTurnIDChars = 64
)

// Routes are the undo routes.
type Routes struct {
	Changes Changes
	Errors  errorenvelope.Writer
}

// Register mounts both.
func (r Routes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "revert-agent-action", Method: http.MethodPost, Path: RevertPath,
		Summary: "Undo one change the assistant made", Tags: []string{"agent"},
	}, func(_ context.Context, _ *struct {
		ThreadID string `path:"thread_id"`
		ActionID string `path:"action_id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.revert}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "revert-agent-turn", Method: http.MethodPost, Path: RevertTurnPath,
		Summary: "Undo every change the assistant made in one turn", Tags: []string{"agent"},
	}, func(_ context.Context, _ *struct {
		ThreadID   string `path:"thread_id"`
		ChatTurnID string `path:"chat_turn_id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.revertTurn}, nil
	})
}

// exchange is one request's caller, ids and answer.
type exchange struct {
	r         Routes
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	caller    principal.Principal
}

func (r Routes) open(ctx huma.Context) (*exchange, bool) {
	x := &exchange{r: r, ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())}
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		x.fail("the route ran without a caller", nil)
		return nil, false
	}
	x.caller = caller
	return x, true
}

func (x *exchange) fail(reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		attributes := []any{slog.String("request_id", x.requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		x.r.Changes.Logger.ErrorContext(x.ctx.Context(), "an undo could not be served", attributes...)
	}
	x.r.Errors.Write(x.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, x.requestID)
}

func (x *exchange) send(status int, body any) {
	x.response.Header().Set("Content-Type", "application/json")
	x.response.Header().Set("Cache-Control", "private, no-store")
	x.response.WriteHeader(status)
	encoder := json.NewEncoder(x.response)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(body)
}

// pathID reads a path id. One too wide for a bigint names nothing, so it
// reads as 0 and is answered "not found"; one that is not digits at all is
// a validation failure, as for every other id route.
func pathID(text string) (int64, bool) {
	id, err := strconv.ParseInt(text, 10, 64)
	if text == "" || err != nil && strings.Trim(text, "0123456789") != "" {
		return 0, false
	}
	return id, true
}

func invalid(name string) errorenvelope.Failure {
	return errorenvelope.Failure{Location: []string{"path", name}, Message: "Input should be a valid integer"}
}

// owned answers 404 for a thread that is not the caller's.
func (x *exchange) owned(threadID int64) bool {
	owned, err := New(x.r.Changes.Pool).ThreadIsOwned(x.ctx.Context(), ThreadIsOwnedParams{ID: threadID, UserID: x.caller.UserID})
	if err != nil {
		x.fail("could not look for the thread", err)
		return false
	}
	if !owned {
		x.r.Errors.Write(x.response, http.StatusNotFound, threadAbsent, x.requestID)
	}
	return owned
}

// undoTimeout bounds one request's undos: a turn's handful of
// transactions and their indexing jobs, each milliseconds.
const undoTimeout = 30 * time.Second

// undoContext is the undo's own context, not the request's: an undo that
// has begun is finished even if the person closes the tab, because its
// indexing must still be queued once its transaction commits, and nobody
// repeats a request they walked away from.
func undoContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), undoTimeout)
}

func (r Routes) revert(ctx huma.Context) {
	x, ok := r.open(ctx)
	if !ok {
		return
	}
	threadID, threadOK := pathID(ctx.Param("thread_id"))
	actionID, actionOK := pathID(ctx.Param("action_id"))
	var failures []errorenvelope.Failure
	if !threadOK {
		failures = append(failures, invalid("thread_id"))
	}
	if !actionOK {
		failures = append(failures, invalid("action_id"))
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(x.response, x.requestID, failures)
		return
	}
	if !x.owned(threadID) {
		return
	}
	work, done := undoContext(ctx.Context())
	defer done()
	result, err := r.Changes.undo(work, owner{userID: x.caller.UserID, threadID: threadID}, actionID)
	if err != nil {
		x.fail("could not undo an action", err)
		return
	}
	switch result.outcome {
	case undone:
		x.send(http.StatusOK, struct {
			Status      string `json:"status"`
			Message     string `json:"message"`
			NewActionID int64  `json:"new_action_id"`
		}{"ok", result.message, result.newAction})
	case absent:
		r.Errors.Write(x.response, http.StatusNotFound, actionAbsent, x.requestID)
	default:
		// Already undone, not undoable, changed since: the action stays as
		// it is, and the sentence says why.
		r.Errors.Write(x.response, http.StatusConflict, result.message, x.requestID)
	}
}

// turnResult is one action of a turn's undo, as the page reads it
// (soloclef_web/lib/apis/agent-actions-api.service.ts).
type turnResult struct {
	ActionID    int64  `json:"action_id"`
	ToolName    string `json:"tool_name"`
	Status      string `json:"status"`
	Message     string `json:"message,omitempty"`
	NewActionID *int64 `json:"new_action_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

// turnAnswer is the whole turn's: every count, so that total is always the
// sum of the others, and "ok" only when nothing was left undone.
type turnAnswer struct {
	Status           string       `json:"status"`
	ChatTurnID       string       `json:"chat_turn_id"`
	Total            int          `json:"total"`
	Reverted         int          `json:"reverted"`
	AlreadyReverted  int          `json:"already_reverted"`
	NotReversible    int          `json:"not_reversible"`
	PermissionDenied int          `json:"permission_denied"`
	Failed           int          `json:"failed"`
	Skipped          int          `json:"skipped"`
	Results          []turnResult `json:"results"`
}

// revertTurn undoes a turn's actions newest first, each in its own
// transaction, so that one that cannot be undone does not hold back the
// rest. Partial success is the usual case and is answered 200 with each
// action's outcome.
func (r Routes) revertTurn(ctx huma.Context) {
	x, ok := r.open(ctx)
	if !ok {
		return
	}
	threadID, threadOK := pathID(ctx.Param("thread_id"))
	turnID := ctx.Param("chat_turn_id")
	var failures []errorenvelope.Failure
	if !threadOK {
		failures = append(failures, invalid("thread_id"))
	}
	if turnID == "" || len(turnID) > maxTurnIDChars {
		failures = append(failures, errorenvelope.Failure{Location: []string{"path", "chat_turn_id"}, Message: "chat_turn_id must be 1 to 64 characters"})
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(x.response, x.requestID, failures)
		return
	}
	if !x.owned(threadID) {
		return
	}
	work, done := undoContext(ctx.Context())
	defer done()
	who := owner{userID: x.caller.UserID, threadID: threadID}
	actions, err := New(r.Changes.Pool).TurnActions(work, TurnActionsParams{UserID: who.userID, ThreadID: threadID, ChatTurnID: turnID})
	if err != nil {
		x.fail("could not read the turn's actions", err)
		return
	}
	answer := turnAnswer{ChatTurnID: turnID, Total: len(actions), Results: make([]turnResult, 0, len(actions))}
	for _, action := range actions {
		entry := turnResult{ActionID: action.ID, ToolName: action.ToolName}
		result, err := r.Changes.undo(work, who, action.ID)
		switch {
		case err != nil:
			r.Changes.Logger.ErrorContext(work, "an action of a turn could not be undone", "request_id", x.requestID, "action_id", action.ID, "error", err.Error())
			entry.Status, entry.Error = "failed", errorenvelope.GenericServerMessage
			answer.Failed++
		case result.outcome == undone:
			entry.Status, entry.Message, entry.NewActionID = "reverted", result.message, &result.newAction
			answer.Reverted++
		case result.outcome == alreadyUndone:
			entry.Status, entry.Message = "already_reverted", result.message
			if result.newAction != 0 {
				entry.NewActionID = &result.newAction
			}
			answer.AlreadyReverted++
		default:
			entry.Status, entry.Message = "not_reversible", result.message
			answer.NotReversible++
		}
		answer.Results = append(answer.Results, entry)
	}
	answer.Status = "ok"
	if answer.Failed > 0 || answer.NotReversible > 0 {
		answer.Status = "partial"
	}
	x.send(http.StatusOK, answer)
}
