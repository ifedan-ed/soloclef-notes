package agentactions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Route is the list route.
type Route struct {
	Pool   *pgxpool.Pool
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// action is one row as the chat page reads it
// (soloclef_web/lib/apis/agent-actions-api.service.ts).
type action struct {
	ID         int64           `json:"id"`
	ThreadID   int64           `json:"thread_id"`
	UserID     string          `json:"user_id"`
	ToolName   string          `json:"tool_name"`
	Args       json.RawMessage `json:"args"`
	ResultID   *string         `json:"result_id"`
	Reversible bool            `json:"reversible"`
	Descriptor json.RawMessage `json:"reverse_descriptor"`
	Error      json.RawMessage `json:"error"`
	ReverseOf  *int64          `json:"reverse_of"`
	RevertedBy *int64          `json:"reverted_by_action_id"`
	IsRevert   bool            `json:"is_revert_action"`
	ToolCallID *string         `json:"tool_call_id"`
	ChatTurnID *string         `json:"chat_turn_id"`
	Created    time.Time       `json:"created_at"`
}

var null = json.RawMessage("null")

// object passes a column on only when it holds a JSON object, which is what
// the client reads it as; anything else (SQL NULL, a JSON null, a list) is
// null, so one odd row cannot fail the client's reading of the whole page.
func object(column []byte) json.RawMessage {
	if trimmed := strings.TrimSpace(string(column)); strings.HasPrefix(trimmed, "{") {
		return json.RawMessage(trimmed)
	}
	return null
}

func actionOf(row ThreadActionsRow) action {
	return action{
		ID: row.ID, ThreadID: row.ThreadID, UserID: row.UserID, ToolName: row.ToolName,
		Args: object(row.Args), ResultID: row.ResultID, Reversible: row.Reversible,
		Descriptor: object(row.ReverseDescriptor), Error: object(row.Error),
		ReverseOf: row.ReverseOf, RevertedBy: row.RevertedByActionID, IsRevert: row.ReverseOf != nil,
		ToolCallID: row.ToolCallID, ChatTurnID: row.ChatTurnID, Created: row.CreatedAt.UTC(),
	}
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-thread-actions", Method: http.MethodGet, Path: ListPath,
		Summary: "What the assistant changed in a thread", Tags: []string{"agent"},
	}, func(_ context.Context, _ *struct {
		ThreadID string `path:"thread_id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	response, requestID := humawriter.New(ctx), requestcontext.ID(ctx.Context())
	fail := func(reason string, cause error) {
		if !errors.Is(cause, context.Canceled) {
			attributes := []any{slog.String("request_id", requestID), slog.String("reason", reason)}
			if cause != nil {
				attributes = append(attributes, slog.String("error", cause.Error()))
			}
			r.Logger.ErrorContext(ctx.Context(), "the action log could not be served", attributes...)
		}
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
	}
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		fail("the route ran without a caller", nil)
		return
	}

	var failures []errorenvelope.Failure
	// An id too wide for a bigint names no thread, so it reads as 0.
	threadText := ctx.Param("thread_id")
	threadID, err := strconv.ParseInt(threadText, 10, 64)
	if threadText == "" || err != nil && strings.Trim(threadText, "0123456789") != "" {
		failures = append(failures, errorenvelope.Failure{Location: []string{"path", "thread_id"}, Message: "Input should be a valid integer"})
	}
	number := func(name string, fallback, low, high int) int {
		text := ctx.Query(name)
		if text == "" {
			return fallback
		}
		n, err := strconv.Atoi(text)
		if err != nil || n < low || n > high {
			failures = append(failures, errorenvelope.Failure{Location: []string{"query", name},
				Message: fmt.Sprintf("%s must be a whole number from %d to %d", name, low, high)})
		}
		return n
	}
	page := number("page", 0, 0, maxPage)
	size := number("page_size", defaultPageSize, 1, maxPageSize)
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	q := New(r.Pool)
	rows, err := q.ThreadActions(ctx.Context(), ThreadActionsParams{
		UserID: caller.UserID, ThreadID: threadID, PageOffset: int32(page * size), PageSize: int32(size)})
	if err != nil {
		fail("could not read the thread's actions", err)
		return
	}
	var total int64
	if len(rows) > 0 {
		total = rows[0].Total
	} else {
		// No row to read from: either the thread is not the caller's, or
		// its log is empty, or the page is past its end.
		log, err := q.ThreadLogSize(ctx.Context(), ThreadLogSizeParams{ThreadID: threadID, UserID: caller.UserID})
		if err != nil {
			fail("could not look for the thread", err)
			return
		}
		if !log.Owned {
			r.Errors.Write(response, http.StatusNotFound, threadAbsent, requestID)
			return
		}
		total = log.Total
	}
	answer := struct {
		Items    []action `json:"items"`
		Total    int64    `json:"total"`
		Page     int      `json:"page"`
		PageSize int      `json:"page_size"`
		HasMore  bool     `json:"has_more"`
	}{Items: make([]action, 0, len(rows)), Total: total, Page: page, PageSize: size, HasMore: int64(page+1)*int64(size) < total}
	for _, row := range rows {
		answer.Items = append(answer.Items, actionOf(row))
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "private, no-store")
	response.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(response)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(answer)
}
