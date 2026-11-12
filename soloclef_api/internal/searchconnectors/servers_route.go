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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// ServersRoute serves GET /api/v1/connectors/mcp: the caller's own MCP
// servers, each with its address and headers masked (secrets.go).
type ServersRoute struct {
	Rows   Rows
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ServersRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-mcp-connectors",
		Method:      http.MethodGet,
		Path:        ServersPath,
		Summary:     "The caller's MCP servers",
		Description: "The account's own MCP servers, by name. A server's key is " +
			"masked for everyone, the person who typed it included.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ServersRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the MCP server list could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.caught(noCaller, nil)
		return
	}
	servers, err := r.Rows.Servers(ctx.Context(), caller.UserID)
	if err != nil {
		failed.caught("could not read the account's MCP servers", err)
		return
	}
	shown := make([]*pythonjson.Object, 0, len(servers))
	for _, server := range servers {
		masked, err := shownServer(server)
		if err != nil {
			failed.caught(rowRefused, err)
			return
		}
		shown = append(shown, masked)
	}
	writeModel(failed, response, func() ([]byte, error) {
		items := make([][]byte, 0, len(servers))
		for index, server := range servers {
			item, err := renderServer(server, shown[index])
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return joinList(items), nil
	})
}

// ServerRoute serves GET /api/v1/connectors/mcp/{connector_id}: one of the
// caller's own MCP servers.
type ServerRoute struct {
	Rows   Rows
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ServerRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-mcp-connector",
		Method:      http.MethodGet,
		Path:        ServerPath,
		Summary:     "One MCP server",
		Description: "One of the account's own MCP servers, its key masked. " +
			"Another account's, and a connection that is not an MCP server, are not found.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct {
		ConnectorID string `path:"connector_id" doc:"The MCP server's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ServerRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the MCP server could not be served"}

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
	var server *Connection
	if !connector.tooWide {
		var err error
		if server, err = r.Rows.Server(ctx.Context(), caller.UserID, connector.value); err != nil {
			failed.caught(rowUnread, err)
			return
		}
	}
	if server == nil {
		r.Errors.Write(response, http.StatusNotFound, serverAbsent, requestID)
		return
	}
	masked, err := shownServer(*server)
	if err != nil {
		failed.caught(rowRefused, err)
		return
	}
	writeModel(failed, response, func() ([]byte, error) { return renderServer(*server, masked) })
}
