// Package tooldefinition turns a declared tool into what the MCP SDK needs.
//
// A tool is written once as a name, a title, a description, a policy hint, a
// parameter signature and a handler over bound arguments. This package publishes
// that as the SDK's tool and wraps the handler so that a refused argument or a
// failed call reaches the model as the same text result the Python server sends,
// rather than as a protocol error the client reports in its own words.
package tooldefinition

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
)

// Handler answers one call with the text the model reads.
type Handler func(ctx context.Context, arguments toolschema.Arguments) (string, error)

// Definition is one tool as this server declares it.
type Definition struct {
	Name        string
	Title       string
	Description string
	Annotations *mcp.ToolAnnotations
	Signature   toolschema.Signature
	Handle      Handler
}

// Register publishes the tool on a server.
//
// Server.AddTool is used rather than the generic mcp.AddTool because the generic
// form infers a schema from a Go type and validates against it; the published
// schema and the wording of a refusal are both part of the contract here, so
// both are produced from the declaration instead.
func Register(server *mcp.Server, definition Definition) error {
	schema, err := definition.Signature.JSONSchema()
	if err != nil {
		return fmt.Errorf("tool %s: %w", definition.Name, err)
	}
	server.AddTool(&mcp.Tool{
		Name:        definition.Name,
		Title:       definition.Title,
		Description: definition.Description,
		Annotations: definition.Annotations,
		InputSchema: schema,
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		arguments, err := definition.Signature.Bind(request.Params.Arguments)
		if err != nil {
			return failed(definition.Name, err), nil
		}
		answer, err := definition.Handle(ctx, arguments)
		if err != nil {
			return failed(definition.Name, err), nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: answer}}}, nil
	})
	return nil
}

// failed is the result shape a failed call has: one text part naming the tool
// and the reason, with isError set.
func failed(name string, reason error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("Error executing tool %s: %s", name, reason.Error()),
		}},
	}
}
