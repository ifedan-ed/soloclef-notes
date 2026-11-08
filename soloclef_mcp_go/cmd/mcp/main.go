// Command mcp runs the Soloclef Notes MCP server.
//
// Two transports share one build. Over stdio, a client launches one process per
// user and the key comes from the environment, so it is required there. Over
// streamable HTTP, one process serves many users, each passing its own key per
// request or signing in through the identity provider from claude.ai or ChatGPT;
// the transport's identity middleware enforces either.
//
// For stdio, stdout is the protocol channel, so every log line goes to stderr.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/httptransport"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/settings"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolmanifest"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if len(arguments) > 0 {
		switch arguments[0] {
		case "dump-tools":
			return dumpTools()
		case "selfcheck":
			return selfcheck()
		default:
			return fmt.Errorf("unknown command %q; use dump-tools or selfcheck", arguments[0])
		}
	}

	configuration := settings.FromEnvironment()
	server, _, err := mcpserver.Build(configuration)
	if err != nil {
		return err
	}

	transport := strings.TrimSpace(os.Getenv("SURFSENSE_MCP_TRANSPORT"))
	if transport == "" {
		transport = "stdio"
	}
	switch transport {
	case "streamable-http", "http":
		return serveHTTP(server, configuration)
	case "stdio":
		if configuration.APIKey == "" {
			return fmt.Errorf(
				"SURFSENSE_API_KEY is required for stdio transport. Create an API " +
					"key in Soloclef Notes (Settings -> API) and pass it via the " +
					"SURFSENSE_API_KEY environment variable.")
		}
		return server.Run(context.Background(), &mcp.StdioTransport{})
	default:
		return fmt.Errorf("unknown transport %q", transport)
	}
}

func serveHTTP(server *mcp.Server, configuration settings.Settings) error {
	address := net.JoinHostPort(configuration.Host, fmt.Sprintf("%d", configuration.Port))
	slog.Info("serving MCP over streamable HTTP", "address", address)
	return http.ListenAndServe(address, httptransport.Build(server, configuration, nil))
}

func dumpTools() error {
	tools, err := toolmanifest.Collect()
	if err != nil {
		return err
	}
	dumped, err := toolmanifest.Dump(tools)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(dumped)
	return err
}

func selfcheck() error {
	tools, err := toolmanifest.Collect()
	if err != nil {
		return err
	}
	problems := toolmanifest.Problems(tools)
	for _, problem := range problems {
		fmt.Fprintln(os.Stderr, problem)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s) in the tool manifest", len(problems))
	}
	fmt.Printf("%d tools registered, manifest healthy\n", len(tools))
	return nil
}
