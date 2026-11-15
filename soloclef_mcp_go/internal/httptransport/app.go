package httptransport

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HealthPath is the public probe the container's healthcheck reads.
const HealthPath = "/health"

// MCPPath is where the protocol endpoint is served.
const MCPPath = "/mcp"

// Build returns the handler this server serves: the MCP endpoint wrapped with
// identity and CORS, plus the health probe. Every other path, the OAuth
// discovery documents some clients probe included, is the mux's plain 404:
// an agent connects with an API key, so there is no sign-in to point it at.
func Build(server *mcp.Server) http.Handler {
	endpoint := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		// Stateless: no session state is kept between requests, so any replica
		// can serve any request. SSE responses, rather than JSON, flush headers
		// early, which keeps a long call (a large upload, a slow literature search)
		// from tripping a client timeout.
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: false},
	)

	routes := http.NewServeMux()
	routes.Handle(MCPPath, endpoint)
	routes.Handle(MCPPath+"/", endpoint)
	routes.HandleFunc(HealthPath, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, 200, map[string]string{"status": "ok"})
	})

	return crossOrigin(&identityMiddleware{
		next:        routes,
		publicPaths: map[string]bool{HealthPath: true},
	})
}

// crossOrigin answers a preflight and exposes the session id header, which a
// browser-based client reads.
func crossOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		headers := writer.Header()
		headers.Set("Access-Control-Allow-Origin", "*")
		headers.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		headers.Set("Access-Control-Allow-Headers", "*")
		headers.Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
