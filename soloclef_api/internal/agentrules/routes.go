package agentrules

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Marks is what the routes need of the database; Store in a deployment.
type Marks interface {
	Tools(ctx context.Context, userID string) ([]Tool, error)
	Mark(ctx context.Context, userID string, mark Mark) error
	Unmark(ctx context.Context, userID string, mark Mark) error
}

// Routes are the two routes over the marks.
type Routes struct {
	Marks  Marks
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	stream := func(serve func(huma.Context)) *huma.StreamResponse { return &huma.StreamResponse{Body: serve} }
	huma.Register(api, huma.Operation{
		OperationID: "read-agent-tools", Method: http.MethodGet, Path: ToolsPath, Tags: []string{"agent-permissions"},
		Summary: "Every tool the assistant can use, and whether it asks first",
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.list), nil })
	huma.Register(api, huma.Operation{
		OperationID: "set-agent-tool-ask-first", Method: http.MethodPut, Path: AskFirstPath, Tags: []string{"agent-permissions"},
		Summary:     "Make the assistant ask before one tool, or stop asking",
		Description: `The body is {"tool": name, "connector_id": a connected app's connection or null, "ask_first": true or false}.`,
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.set), nil })
}

// call is one request's common parts.
type call struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	userID    string
}

// begin answers a request with no caller, which is the wiring gone wrong
// and never an unauthenticated read, and, where person is true, one that is
// not a person in a session. ok is false when it has answered.
func (r Routes) begin(ctx huma.Context, person bool) (call, bool) {
	c := call{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())}
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated || caller.UserID == "" {
		r.failed(c, "the route ran without an authenticated caller", nil)
		return c, false
	}
	if person && !caller.IsSession() {
		r.Errors.Write(c.response, http.StatusForbidden, sessionRequired, c.requestID)
		return c, false
	}
	c.userID = caller.UserID
	return c, true
}

func (r Routes) failed(c call, reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", c.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(c.ctx.Context(), slog.LevelError, "an agent permission route failed", attributes...)
	r.Errors.Write(c.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, c.requestID)
}

func (r Routes) list(ctx huma.Context) {
	c, ok := r.begin(ctx, false)
	if !ok {
		return
	}
	tools, err := r.Marks.Tools(ctx.Context(), c.userID)
	if err != nil {
		r.failed(c, "could not read the tools", err)
		return
	}
	body, err := json.Marshal(tools)
	if err != nil {
		r.failed(c, "could not render the tools", err)
		return
	}
	pythonjson.Send(c.response, http.StatusOK, body)
}

func (r Routes) set(ctx huma.Context) {
	c, ok := r.begin(ctx, true)
	if !ok {
		return
	}
	mark, askFirst, valid := r.readBody(c)
	if !valid {
		return
	}
	var err error
	if askFirst {
		err = r.Marks.Mark(ctx.Context(), c.userID, mark)
	} else {
		err = r.Marks.Unmark(ctx.Context(), c.userID, mark)
	}
	switch {
	case errors.Is(err, errToolAbsent):
		r.Errors.Write(c.response, http.StatusNotFound, toolAbsent, c.requestID)
	case err != nil:
		r.failed(c, "could not change the mark", err)
	default:
		pythonjson.SendNoContent(c.response)
	}
}

// readBody reads {"tool", "connector_id", "ask_first"}, answering a body
// that does not validate. A connection id no connection can have (below 1,
// or past the column) names no tool, which is the same "not found" as a tool
// that is not the caller's; so does a name with U+0000, which no tool has
// and no text column can hold.
func (r Routes) readBody(c call) (mark Mark, askFirst, valid bool) {
	body, failure, err := requestbody.Decode(c.ctx.Header("Content-Type"), requestbody.Buffered(c.ctx.Context()))
	if err != nil {
		r.failed(c, "a body reached the route that was never buffered", err)
		return Mark{}, false, false
	}
	if failure != nil {
		r.Errors.WriteValidation(c.response, c.requestID, []errorenvelope.Failure{*failure})
		return Mark{}, false, false
	}
	model := requestbody.NewModel(body)
	mark.Tool = model.Text("tool", 1, maxToolName)
	connector := model.OptionalNumber("connector_id")
	askFirst = model.RequiredTruth("ask_first")
	if !model.Valid() {
		r.Errors.WriteValidation(c.response, c.requestID, model.Failures())
		return Mark{}, false, false
	}
	if strings.ContainsRune(mark.Tool, 0) {
		r.Errors.Write(c.response, http.StatusNotFound, toolAbsent, c.requestID)
		return Mark{}, false, false
	}
	if connector.Value != nil {
		if connector.OutOfRange || *connector.Value < 1 {
			r.Errors.Write(c.response, http.StatusNotFound, toolAbsent, c.requestID)
			return Mark{}, false, false
		}
		mark.ConnectorID = *connector.Value
	}
	return mark, askFirst, true
}
