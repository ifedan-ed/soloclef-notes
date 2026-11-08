package httptransport

import (
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/protectedresource"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/settings"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/signincheck"
)

// HealthPath is the public probe the container's healthcheck reads.
const HealthPath = "/health"

// MCPPath is where the protocol endpoint is served.
const MCPPath = "/mcp"

// ResourceFor builds the protected-resource document's description, or nothing
// when agent sign-in is not configured.
func ResourceFor(configuration settings.Settings) *protectedresource.Resource {
	if !configuration.OAuthEnabled() {
		return nil
	}
	return &protectedresource.Resource{
		Address:             configuration.PublicURL,
		AuthorizationServer: configuration.AuthorizationServer,
		Scopes:              configuration.OAuthScopes,
	}
}

// Build returns the handler this server serves: the MCP endpoint wrapped with
// identity and CORS, plus the health probe and, when sign-in is on, the
// protected-resource document.
func Build(server *mcp.Server, configuration settings.Settings, signIn *signincheck.Check) http.Handler {
	endpoint := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		// Stateless: no session state is kept between requests, so any replica
		// can serve any request. SSE responses, rather than JSON, flush headers
		// early, which keeps a long scraper call from tripping a client timeout.
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: false},
	)

	routes := http.NewServeMux()
	routes.Handle(MCPPath, endpoint)
	routes.Handle(MCPPath+"/", endpoint)
	routes.HandleFunc(HealthPath, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, 200, nil, map[string]string{"status": "ok"})
	})

	publicPaths := map[string]bool{HealthPath: true}
	resource := ResourceFor(configuration)
	if resource != nil {
		document, err := json.Marshal(resource.Metadata())
		if err != nil {
			document = []byte(`{}`)
		}
		for _, path := range resource.MetadataPaths() {
			routes.HandleFunc(path, func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write(document)
			})
			publicPaths[path] = true
		}
		if signIn == nil {
			signIn = signincheck.New(configuration.BaseURL)
		}
	} else {
		signIn = nil
	}

	return crossOrigin(&identityMiddleware{
		next:        routes,
		publicPaths: publicPaths,
		resource:    resource,
		signIn:      signIn,
	})
}

// crossOrigin answers a preflight and exposes the two headers a browser-based
// client reads: the session id, and the challenge that says where to sign in.
func crossOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		headers := writer.Header()
		headers.Set("Access-Control-Allow-Origin", "*")
		headers.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		headers.Set("Access-Control-Allow-Headers", "*")
		headers.Set("Access-Control-Expose-Headers", "Mcp-Session-Id, WWW-Authenticate")
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
