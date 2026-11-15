// Package httptransport wraps the SDK's MCP endpoint with identity and CORS for
// the remote transport.
//
// CORS is outermost so a keyless browser preflight is answered before the
// identity check. /health is a public path, exempt from the credential check.
package httptransport

import (
	"encoding/json"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/callercredential"
)

// identityMiddleware establishes the caller's credential for each request, or
// refuses a request that has none.
//
// The credential is an API key, and it is not judged here: the backend checks
// it on every tool call, so a second check here would only be a second place
// for the two to disagree. A request with no credential at all is refused
// here, so no tool ever runs unauthenticated.
type identityMiddleware struct {
	next        http.Handler
	publicPaths map[string]bool
}

func (m *identityMiddleware) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if m.publicPaths[request.URL.Path] {
		m.next.ServeHTTP(writer, request)
		return
	}
	credential := callercredential.FromHeaders(request.Header)
	if credential == "" {
		writeJSON(writer, 401, map[string]string{
			"error": "unauthorized",
			"message": "Missing Soloclef Notes API key. Send 'Authorization: " +
				"Bearer ss_pat_...' (or an X-API-Key header).",
		})
		return
	}
	m.next.ServeHTTP(writer,
		request.WithContext(callercredential.Bind(request.Context(), credential)))
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	encoded, err := json.Marshal(body)
	if err != nil {
		http.Error(writer, "", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(encoded)
}
