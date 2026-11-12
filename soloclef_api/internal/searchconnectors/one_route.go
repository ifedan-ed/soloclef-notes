package searchconnectors

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// OneRoute serves GET /api/v1/search-source-connectors/{connector_id}: one of
// the caller's own connections. It carries `enabled`, so whether a
// connection is in use needs no second request.
type OneRoute struct {
	Rows   Rows
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r OneRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-search-source-connector",
		Method:      http.MethodGet,
		Path:        OnePath,
		Summary:     "One connection",
		Description: "One of the account's own connections. Another account's " +
			"is not found. A stored credential is never answered.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct {
		ConnectorID string `path:"connector_id" doc:"The connection's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r OneRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the connection could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.caught(noCaller, nil)
		return
	}
	problems := make([]errorenvelope.Failure, 0, 1)
	connector := readNumber(ctx.Param("connector_id"), &problems, "path", "connector_id")
	if len(problems) > 0 {
		r.Errors.WriteValidation(response, requestID, problems)
		return
	}
	var connection *Connection
	if !connector.tooWide {
		var err error
		if connection, err = r.Rows.Connection(ctx.Context(), caller.UserID, connector.value); err != nil {
			failed.caught(rowUnread, err)
			return
		}
	}
	if connection == nil {
		r.Errors.Write(response, http.StatusNotFound, connectorAbsent, requestID)
		return
	}
	answerConnection(failed, response, *connection)
}

// EnabledRoute serves PUT /api/v1/search-source-connectors/{connector_id}/enabled:
// the account's switch for one connection.
//
// Off keeps the connection, its credentials and the documents that came
// through it; the chat stops offering it and the schedule stops indexing it.
// An MCP server switched either way changes which tools the chat may offer,
// so the account's tool counter is moved after the write.
type EnabledRoute struct {
	Changes Changes
	Tools   ToolChanges
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

func (r EnabledRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "set-search-source-connector-enabled",
		Method:      http.MethodPut,
		Path:        EnabledPath,
		Summary:     "Switch a connection on or off",
		Description: "Body: {\"enabled\": true|false}. Answers the connection as it then stands.",
		Tags:        []string{"connectors"},
	}, func(ctx context.Context, _ *struct {
		ConnectorID string `path:"connector_id" doc:"The connection's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r EnabledRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the connection could not be switched"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.caught(noCaller, nil)
		return
	}
	problems := make([]errorenvelope.Failure, 0, 2)
	connector := readNumber(ctx.Param("connector_id"), &problems, "path", "connector_id")
	enabled, bodyProblems, decodeErr := readEnabled(ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		failed.caught("a body reached the route that was never buffered", decodeErr)
		return
	}
	if problems = append(problems, bodyProblems...); len(problems) > 0 {
		r.Errors.WriteValidation(response, requestID, problems)
		return
	}
	var connection *Connection
	if !connector.tooWide {
		var err error
		if connection, err = r.Changes.SetEnabled(ctx.Context(), caller.UserID, connector.value, enabled); err != nil {
			failed.caught("could not switch the connection", err)
			return
		}
	}
	if connection == nil {
		r.Errors.Write(response, http.StatusNotFound, connectorAbsent, requestID)
		return
	}
	if connection.IsServer() {
		toolsChanged(ctx.Context(), r.Tools, r.Logger, requestID, caller.UserID)
	}
	answerConnection(failed, response, *connection)
}

// readEnabled reads the switch's body: one required boolean and nothing else.
func readEnabled(contentType string, body []byte) (bool, []errorenvelope.Failure, error) {
	if len(body) == 0 {
		return false, []errorenvelope.Failure{{
			Location: []string{requestbody.BodyRoot},
			Message:  requestbody.FieldRequired,
		}}, nil
	}
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return false, nil, err
	}
	if failure != nil {
		return false, []errorenvelope.Failure{*failure}, nil
	}
	model := requestbody.NewModel(object)
	enabled := model.RequiredTruth("enabled")
	model.RefuseExtras("enabled")
	if !model.Valid() {
		return false, model.Failures(), nil
	}
	return enabled, nil, nil
}

// answerConnection renders one connection and sends it.
func answerConnection(failed failures, response http.ResponseWriter, connection Connection) {
	config, err := shownConfig(connection)
	if err != nil {
		failed.caught(rowRefused, err)
		return
	}
	writeModel(failed, response, func() ([]byte, error) { return renderConnection(connection, config) })
}

// writeModel sends a rendered answer, or the 500 for one that could not be
// rendered.
func writeModel(failed failures, response http.ResponseWriter, render func() ([]byte, error)) {
	body, err := render()
	if err != nil {
		failed.uncaught(bodyUnwritten, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
