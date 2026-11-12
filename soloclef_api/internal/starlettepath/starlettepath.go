// Package starlettepath is how the Python tier's router reads a request path,
// for a tier whose own router reads it differently.
//
// The two routers disagree before any route runs, and about one thing: *what
// the segments of a path are*.
//
//   - uvicorn percent-decodes the whole path and hands Starlette the result, so
//     Starlette splits the **decoded** path. "ab%2Fcd" is two segments there. A
//     path parameter is `[^/]+` over that, so it is never empty and never holds
//     a slash, and "." and ".." are ordinary values. Nothing is tidied: a path
//     with "//" in it simply matches no route.
//   - Go's ServeMux splits the **escaped** path and unescapes each segment
//     afterwards, so "ab%2Fcd" is one segment and the handler is handed
//     "ab/cd". And before it matches anything it *cleans* the path -- "//",
//     "/./" and "/../" -- and answers a 301 to the cleaned one, without
//     dispatching.
//
// So a route of this tier can be run for a path the Python tier has no route
// for, and a path the Python tier answers can be redirected away here. What
// Starlette answers when no route matches is not this app's error envelope
// either (RouterNotFound), so the difference is visible in the status and in
// every byte of the body.
//
// There are two places the mirror is applied, and each exists because the
// other cannot do its job:
//
//   - a **route** asks Unroutable about its own parameters and answers
//     WriteRouterNotFound, then reads them through AsDecodedByTheServer. That
//     is what makes a route right by itself, and it is all a route mounted
//     alone needs.
//   - the **Guard** stands in front of the whole mux (guard.go). A route
//     cannot stop ServeMux redirecting, because ServeMux redirects before it
//     dispatches; and a route behind the credential gate cannot answer the
//     router's 404 for a caller with no credential, because the gate's 401
//     comes first here and the router's 404 comes first there.
//
// Measured against the real Python app, not read off it: see
// custom/docs/go-api-tier.md, "The two routers do not segment a path the same
// way". The recording of that measurement was deleted on 2026-11-11 with the
// rule that every answer match Python's; what the guard must do for every
// served route is now written as rules in cmd/api/router_test.go.
package starlettepath

import (
	"net/http"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// RouterNotFound is the body Starlette's router writes for a path no route
// matches. It is not this app's error envelope: the envelope is written by
// the handler app.py registers for *FastAPI's* HTTPException, and the router
// raises Starlette's, which that handler is not registered for. So the body
// is the bare default -- 22 bytes, application/json -- with the CORS headers
// and the request id every response gets. It keeps its CORS headers because
// the router sits *inside* Starlette's CORSMiddleware; this is not one of the
// 500s that are answered from outside it (internal/corsmirror.Suppress).
const RouterNotFound = `{"detail":"Not Found"}`

// Unroutable is whether a path parameter holds something Starlette's router
// could not have matched as one segment.
//
// A parameter there is `[^/]+` over the *decoded* path: uvicorn unquotes the
// path before the router sees it, so "ab%2Fcd" is two segments and matches no
// route with one parameter in that place. Go's ServeMux matches segment by
// segment before unescaping, hands the handler "ab/cd", and would have the
// route answer in its own words where the Python tier answers the router's
// 404. "%2f" is the same slash: the escape's case does not survive decoding.
//
// An empty parameter is not asked about here. Neither router dispatches one:
// Starlette matches no route, and ServeMux never reaches a handler with it --
// it redirects, which is the Guard's to answer, not a route's.
func Unroutable(parameters ...string) bool {
	for _, parameter := range parameters {
		if strings.Contains(parameter, "/") {
			return true
		}
	}
	return false
}

// WriteRouterNotFound answers as Starlette's router does (RouterNotFound).
func WriteRouterNotFound(response http.ResponseWriter, requestID string) {
	response.Header().Set("X-Request-ID", requestID)
	pythonjson.Send(response, http.StatusNotFound, []byte(RouterNotFound))
}

// AsDecodedByTheServer is a path parameter as the Python tier's server hands
// it to the route. uvicorn decodes the percent-escapes as UTF-8 and replaces
// what is not UTF-8 with U+FFFD rather than refusing it, so a parameter of
// "ab%FFcd" is looked up as "ab�cd" and names nothing. Go keeps the raw
// byte, and Postgres would refuse a statement carrying it -- a 500 where the
// Python tier answers that the thing was not found.
//
// Go and Python do not always agree on *how many* replacement characters a
// malformed run becomes. That cannot matter to a caller that only compares
// the parameter with stored text: what was not valid UTF-8 matches no stored
// value however it is repaired, so it is never echoed into an answer. A route
// that would *echo* the parameter must not rely on this function for the
// count.
func AsDecodedByTheServer(parameter string) string {
	return strings.ToValidUTF8(parameter, "�")
}
