// Package corsmirror answers a cross-origin request with the same headers
// Starlette's CORSMiddleware puts on the Python tier's responses.
//
// Only a simple request needs this: the proxy hands Go one GET route, so a
// preflight (OPTIONS) is still answered by the Python tier, which owns the
// whole CORS policy. What a browser or the mobile app needs back from a GET is
// the echoed origin, the credentials flag, the exposed header and Vary.
package corsmirror

import (
	"net/http"
	"regexp"
	"strings"
)

// loopbackOrigins is app.py's allow_origin_regex, which lets a self-hosted
// deployment be reached on localhost without configuring anything.
var loopbackOrigins = regexp.MustCompile(`^https?://(localhost|127\.0\.0\.1)(:\d+)?$`)

// alwaysAllowed are the two origins app.py appends for local development and
// the desktop app. They name no server.
var alwaysAllowed = []string{"http://localhost:3000", "http://127.0.0.1:3000"}

// exposedHeaders is app.py's expose_headers.
const exposedHeaders = "Content-Disposition"

// Policy is the allowlist. Origins come from configuration, because every
// entry the Python tier computes comes from NEXT_FRONTEND_URL.
type Policy struct {
	allowed map[string]bool
}

// NewPolicy takes the origins this deployment allows, in the form the Python
// tier builds: the frontend URL, with and without a trailing slash, and its
// www variant.
func NewPolicy(origins []string) Policy {
	allowed := map[string]bool{}
	for _, origin := range append(origins, alwaysAllowed...) {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			allowed[trimmed] = true
		}
	}
	return Policy{allowed: allowed}
}

// Allows reports whether this origin gets CORS headers back.
func (p Policy) Allows(origin string) bool {
	return p.allowed[origin] || loopbackOrigins.MatchString(origin)
}

// Middleware adds the headers to an allowed cross-origin request's response. A
// request with no Origin, or one from an origin that is not allowed, is served
// without them, exactly as Starlette serves it.
//
// The headers are written when the response **starts**, not before the handler
// runs, because whether they belong on it is not settled until then: a response
// the Python tier would have written from outside its own CORS middleware
// carries none, and the handler is what knows that (suppress.go). It also keeps
// a forwarded response's headers the upstream's rather than doubling them.
func (p Policy) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		request = request.WithContext(WithSuppression(request.Context()))
		ctx := request.Context()

		deferred := &deferredHeaders{ResponseWriter: response, add: func(header http.Header) {
			if origin == "" || suppressed(ctx) {
				return
			}
			// Starlette's simple_headers go on every request that carries an
			// Origin, allowed or not; only the echoed origin and Vary are
			// conditional (CORSMiddleware.send, allow_explicit_origin).
			header.Set("Access-Control-Allow-Credentials", "true")
			header.Set("Access-Control-Expose-Headers", exposedHeaders)
			if p.Allows(origin) {
				header.Set("Access-Control-Allow-Origin", origin)
				header.Add("Vary", "Origin")
			}
		}}
		next.ServeHTTP(deferred, request)
	})
}
