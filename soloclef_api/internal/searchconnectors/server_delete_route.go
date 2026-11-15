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

// ServerDeleteRoute serves DELETE /api/v1/connectors/mcp/{connector_id}.
//
// One statement decides and acts: it deletes the row only if it is an MCP
// server of the caller's, and the row it returns is the answer. There is
// nothing to check first and so nothing that can change between a check and
// the delete. A server signed in to with OAuth then has its tokens revoked
// at the authorization server, best effort, from the config the delete
// returned: disconnecting ends the sign-in there too.
type ServerDeleteRoute struct {
	Changes    Changes
	Revocation Revoker
	Tools      ToolChanges
	Errors     errorenvelope.Writer
	Logger     *slog.Logger
}

func (r ServerDeleteRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-mcp-connector",
		Method:      http.MethodDelete,
		Path:        ServerPath,
		Summary:     "Remove an MCP server",
		Description: "Removes the caller's own server, and tells every process that offers " +
			"the account's MCP tools to a model that they have changed.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct {
		ConnectorID string `path:"connector_id" doc:"The MCP server's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ServerDeleteRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the MCP server could not be removed"}

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
	var config []byte
	if !connector.tooWide {
		var err error
		if config, err = r.Changes.DeleteServer(ctx.Context(), caller.UserID, connector.value); err != nil {
			failed.caught("could not delete the MCP server", err)
			return
		}
	}
	if config == nil {
		r.Errors.Write(response, http.StatusNotFound, serverAbsent, requestID)
		return
	}
	// After the delete has committed, so no process rebuilds its tool list
	// from a row that is about to go.
	toolsChanged(ctx.Context(), r.Tools, r.Logger, requestID, caller.UserID)
	// The row is gone whatever the provider says; the person is waiting
	// only for the revocation's bounded time.
	r.Revocation.Revoke(context.WithoutCancel(ctx.Context()), caller.UserID, connector.value, serverTypeTag, config)
	pythonjson.SendNoContent(response)
}
