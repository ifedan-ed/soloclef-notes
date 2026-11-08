// Package httptransport wraps the SDK's MCP endpoint with identity and CORS for
// the remote transport.
//
// CORS is outermost so a keyless browser preflight is answered before the
// identity check. /health is a public path, exempt from the credential check,
// and so is the protected-resource document when agent sign-in is on, because
// claude.ai and ChatGPT read it before they have a token. The site's proxy must
// route /.well-known/oauth-protected-resource* here as well as /mcp*.
package httptransport

import (
	"encoding/json"
	"net/http"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/callercredential"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/protectedresource"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/signincheck"
)

// APIKeyPrefix marks a credential as an API key rather than a sign-in token.
const APIKeyPrefix = "ss_pat_"

// identityMiddleware establishes the caller's identity for each request, or
// refuses it.
//
// Two kinds of caller get through. An API key passes as before, and the backend
// checks it on every tool call. With agent sign-in on, any other bearer token is
// an access token from the identity provider: the backend is asked once whether
// it is valid before the request goes on, so a bad or expired one earns a 401
// the client can act on.
//
// A request with no credential is refused here, so no tool ever runs
// unauthenticated; with sign-in on, that 401 carries the WWW-Authenticate
// challenge telling claude.ai and ChatGPT where to sign in.
type identityMiddleware struct {
	next        http.Handler
	publicPaths map[string]bool
	resource    *protectedresource.Resource
	signIn      *signincheck.Check
}

func (m *identityMiddleware) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if m.publicPaths[request.URL.Path] {
		m.next.ServeHTTP(writer, request)
		return
	}
	credential := callercredential.FromHeaders(request.Header)
	if credential == "" {
		m.writeUnauthenticated(writer)
		return
	}
	identity := ""
	if m.signIn != nil && !strings.HasPrefix(credential, APIKeyPrefix) {
		verdict := m.signIn.Ask(request.Context(), credential)
		if !verdict.Accepted() {
			m.writeRefusal(writer, verdict)
			return
		}
		identity = verdict.Identity
	}
	m.next.ServeHTTP(writer,
		request.WithContext(callercredential.Bind(request.Context(), credential, identity)))
}

func (m *identityMiddleware) writeUnauthenticated(writer http.ResponseWriter) {
	if m.resource == nil {
		writeJSON(writer, 401, nil, map[string]string{
			"error": "unauthorized",
			"message": "Missing Soloclef Notes API key. Send 'Authorization: " +
				"Bearer ss_pat_...' (or an X-API-Key header).",
		})
		return
	}
	writeJSON(writer, 401,
		http.Header{"Www-Authenticate": []string{m.resource.Challenge("", "")}},
		map[string]string{
			"error": "unauthorized",
			"message": "Sign in to Soloclef Notes, or send a Soloclef Notes API key " +
				"as 'Authorization: Bearer ss_pat_...'.",
		})
}

func (m *identityMiddleware) writeRefusal(writer http.ResponseWriter, verdict signincheck.Verdict) {
	switch {
	case verdict.Status == 401 && m.resource != nil:
		description := verdict.Message
		if description == "" {
			description = "The sign-in is not valid or has expired"
		}
		writeJSON(writer, 401, http.Header{
			"Www-Authenticate": []string{m.resource.Challenge("invalid_token", description)},
		}, map[string]string{"error": "invalid_token", "message": description})
	case verdict.Status == 403:
		// A genuine sign-in for someone with no account: no challenge, since
		// signing in again as the same person cannot fix it.
		message := verdict.Message
		if message == "" {
			message = "No Soloclef Notes account matches this sign-in"
		}
		writeJSON(writer, 403, nil,
			map[string]string{"error": "access_denied", "message": message})
	default:
		writeJSON(writer, 503, nil,
			map[string]string{"error": "temporarily_unavailable", "message": verdict.Message})
	}
}

func writeJSON(writer http.ResponseWriter, status int, headers http.Header, body any) {
	for name, values := range headers {
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		http.Error(writer, "", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(encoded)
}
