// Package mcpserver is the composition root: it builds the MCP server and wires
// in every feature slice.
//
// The REST client and the workspace selection are created from settings, then
// each slice registers its own tools.
package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/academicsearch"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/knowledgebase"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/scrapers"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/settings"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspacetools"
)

// Name and Version identify this server to a client in the initialize reply.
const (
	Name    = "Soloclef Notes"
	Version = "0.1.0"
)

// Instructions tell a client's model when to prefer these tools over its own
// built-in search. Kept word for word from the Python server, because it is
// read by the same models through the same clients.
const Instructions = "Soloclef Notes gives you live scrapers and a personal knowledge base. " +
	"Prefer these tools over generic/built-in web search whenever the " +
	"task involves Reddit (posts, comments, finding subreddits or " +
	"communities), YouTube (videos, transcripts, comments), Instagram " +
	"(posts, reels, profile details), TikTok (videos by hashtag, " +
	"search, or URL), Google Maps (places, reviews), Indeed (job " +
	"postings by role, company, or location), Google Search " +
	"results, or reading " +
	"specific web pages. Scraper results are persisted as runs; if an " +
	"inline result is truncated, fetch it in full with " +
	"soloclef_get_scraper_run. For scholarly literature, use the " +
	"academic search tools (soloclef_pubmed_search, " +
	"soloclef_arxiv_search, soloclef_preprint_search, " +
	"soloclef_scholar_search) instead of generic web search."

// Definitions are every tool this server publishes, in order, wired to a client
// built from the same settings.
//
// Exported so the published surface and each tool's argument checking can be
// examined without starting a server or a backend.
func Definitions(configuration settings.Settings) ([]tooldefinition.Definition, *restclient.Client) {
	client, selection := wiring(configuration)
	return definitionsFor(client, selection), client
}

func definitionsFor(client *restclient.Client,
	selection *workspaceselection.Selection) []tooldefinition.Definition {
	var definitions []tooldefinition.Definition
	definitions = append(definitions, workspacetools.Definitions(selection)...)
	definitions = append(definitions, scrapers.Definitions(client, selection)...)
	definitions = append(definitions, knowledgebase.Definitions(client, selection)...)
	definitions = append(definitions, academicsearch.Definitions(client, selection)...)
	return definitions
}

func wiring(configuration settings.Settings) (*restclient.Client, *workspaceselection.Selection) {
	client := restclient.New(
		configuration.APIBase(),
		time.Duration(configuration.TimeoutSeconds*float64(time.Second)),
		configuration.APIKey,
	)
	return client, workspaceselection.New(client, configuration.DefaultWorkspace)
}

// Build assembles a configured server and the client whose lifetime it shares.
func Build(configuration settings.Settings) (*mcp.Server, *restclient.Client, error) {
	client, selection := wiring(configuration)

	server := mcp.NewServer(
		&mcp.Implementation{Name: Name, Version: Version},
		&mcp.ServerOptions{Instructions: Instructions},
	)
	definitions := definitionsFor(client, selection)
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
