// Package mcpserver is the composition root: it builds the MCP server and wires
// in every feature slice.
//
// The REST client is created from settings, then each slice registers its own
// tools. There is no selection of any kind: the caller's API key or sign-in is
// its account, and the account is the whole scope of every tool.
package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/academicsearch"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/knowledgebase"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/settings"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
)

// Name and Version identify this server to a client in the initialize reply.
const (
	Name    = "Soloclef Notes"
	Version = "0.1.0"
)

// Instructions tell a client's model when to reach for these tools rather than
// its own built-in search: stored notes first, then scholarly literature. They
// name only tools this server registers, because a model that is pointed at a
// missing tool spends a turn on an "Unknown tool" reply.
const Instructions = "Soloclef Notes gives you a personal knowledge base and " +
	"academic literature search. When a question might be answered by notes, " +
	"files or pages the user has already stored, search them first with " +
	"soloclef_search_knowledge_base, and save findings worth keeping with " +
	"soloclef_add_document. For scholarly literature, use the academic " +
	"search tools (soloclef_pubmed_search, soloclef_arxiv_search, " +
	"soloclef_preprint_search) instead of generic " +
	"web search."

// Definitions are every tool this server publishes, in order, wired to a client
// built from the same settings.
//
// Exported so the published surface and each tool's argument checking can be
// examined without starting a server or a backend.
func Definitions(configuration settings.Settings) ([]tooldefinition.Definition, *restclient.Client) {
	client := newClient(configuration)
	return definitionsFor(client), client
}

func definitionsFor(client *restclient.Client) []tooldefinition.Definition {
	var definitions []tooldefinition.Definition
	definitions = append(definitions, knowledgebase.Definitions(client)...)
	definitions = append(definitions, academicsearch.Definitions(client)...)
	return definitions
}

func newClient(configuration settings.Settings) *restclient.Client {
	return restclient.New(
		configuration.APIBase(),
		time.Duration(configuration.TimeoutSeconds*float64(time.Second)),
		configuration.APIKey,
	)
}

// Build assembles a configured server and the client whose lifetime it shares.
func Build(configuration settings.Settings) (*mcp.Server, *restclient.Client, error) {
	client := newClient(configuration)

	server := mcp.NewServer(
		&mcp.Implementation{Name: Name, Version: Version},
		&mcp.ServerOptions{Instructions: Instructions},
	)
	definitions := definitionsFor(client)
	order := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		if err := tooldefinition.Register(server, definition); err != nil {
			return nil, nil, err
		}
		order = append(order, definition.Name)
	}

	server.AddReceivingMiddleware(unknownToolAsResult)
	server.AddReceivingMiddleware(listInRegistrationOrder(order))
	return server, client, nil
}

// unknownToolAsResult answers a call to a tool this server does not have with a
// text result rather than a protocol error.
//
// The SDK refuses an unknown tool with JSON-RPC -32602, which a client reports
// in its own words; the Python server answers "Unknown tool: <name>" as an
// errored tool result, which is what the model is used to reading.
func unknownToolAsResult(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, request)
		call, isCall := request.(*mcp.CallToolRequest)
		if err == nil || !isCall || !isUnknownTool(err) {
			return result, err
		}
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("Unknown tool: %s", call.Params.Name),
			}},
		}, nil
	}
}
