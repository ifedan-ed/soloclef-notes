package starlettepath

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Guard stands in front of the server's ServeMux and settles, before the mux
// sees a request, the paths the two routers would segment differently.
//
// It has to be in front and not in a handler for two reasons, and each was
// measured rather than assumed (cmd/api/router_test.go):
//
//   - ServeMux **redirects before it dispatches**. `/api/v1/prompts//copy`
//     is answered 301 to `/api/v1/prompts/copy` and no handler, and so no
//     wrapper on a handler, ever runs. Starlette cleans nothing and answers
//     its bare 404.
//   - most of this tier's routes are behind the credential gate. A request
//     for `/api/v1/notebooks/a%2Fb/sources` with no credential is a **401**
//     here, because the gate comes before the route, and a **404** on the
//     Python tier, because its router comes before its dependencies. No check
//     inside the route can be early enough.
//
// # Scope: exactly what it changes
//
// A request passes through **untouched** unless the two routers disagree
// about its path, which is only when ServeMux would clean it (an empty
// segment, "." or "..") or when an escaped slash makes Go's segments differ
// from the decoded path's. Every ordinary request -- every request the web
// app or a client library sends -- is neither, so nothing about a path either
// router already agreed on is different: not the health probe, not a served
// route, and not a request the gate hands to the Python tier.
//
// For a path they do disagree about, there are three outcomes and the
// decision is made against **the routes this tier serves and nothing else**
// (Served), because that is the only route table this tier has:
//
//  1. The decoded path is one of this tier's routes as Starlette matches it.
//     The Python tier would run that route, so this tier does too: the
//     request goes on with its path re-escaped so that ServeMux sees the
//     segments Starlette sees. `/api/v1/notebooks/7%2Fsources` is the
//     sources of notebook 7 on both tiers, and `/api/v1/folders/./breadcrumb`
//     is the breadcrumb of the folder whose id is "." on both.
//
//  2. It is not, but ServeMux would have had this tier answer for one of its
//     routes anyway -- by running one with a slash inside a parameter, or by
//     redirecting a path whose only fault is an empty segment towards one.
//     Starlette matches no route for such a path, so the answer is NotFound.
//
//  3. Anything else is left to ServeMux exactly as it was before this guard
//     existed. That is every disagreeing path that names no route of this
//     tier (the proxy does not send those), and two classes this tier cannot
//     settle from its own route table, which are listed below.
//
// # What it deliberately does not settle
//
// "Starlette matches no route" is a claim about the *whole* Python route
// table, and this tier only holds its own part of it. Outcome 2 answers 404
// only where that is safe to say:
//
//   - **An empty segment** can match no Starlette route whatever the table
//     holds, because a parameter is `[^/]+` and a literal is never empty. The
//     one exception is a `{name:path}` parameter, which the Python app has
//     exactly one of (`PUT /api/v1/billing/admin/models/{model_id:path}`) and
//     which no path this guard answers can reach: the guard answers only a
//     path whose non-empty segments are a route of this tier, and none of
//     those starts with that route's literal prefix. NewServed refuses a
//     template of this tier's own that is not plain segments, so this tier
//     cannot grow one unnoticed.
//   - **A slash inside a parameter** is answered 404 although, in principle,
//     the decoded path could be a route only the Python tier has
//     (two of its segments joined by an escaped slash). Measured, every one of
//     this tier's routes answers 404 there for an ordinary value with a slash
//     in it; and the proxy routes on the decoded path, so a path that decodes
//     to a Python-only route is sent to the Python tier and never arrives
//     here. Before this guard such a path ran this tier's route with a
//     parameter no route of either tier accepts, which was right for nobody.
//   - **"." and ".." where no parameter is** (`/api/v1/x/../pats`) are left
//     to ServeMux, which still redirects them. The Python tier answered 404
//     to every one that was measured, but unlike the empty segment nothing
//     makes that true of the whole table: "." is an ordinary parameter value
//     to Starlette, so such a path may be a Python-only route with a
//     parameter of "..". This tier cannot know, and does not guess.
//   - **A trailing slash** (`/api/v1/pats/`, `/api/v1/pats//`, and
//     `/api/v1/pats/7%2F`, which decodes to one) is Starlette's
//     `redirect_slashes`: a 307 to the path without it when that path is
//     some route's, of any method, and the bare 404 when it is nobody's.
//     That is a different mechanism from the router's 404, its answer
//     depends on routes this tier does not hold, and it is not answered
//     here.
type Guard struct {
	// Served is the routes this tier answers, which is all the guard decides
	// against.
	Served Served
	// NotFound answers outcome 2. It is a handler and not a call to
	// WriteRouterNotFound so that the server can put in front of it whatever
	// the Python tier has in front of its router: the CSRF check is outside
	// Starlette's router, so a forged-looking write to an unroutable path is
	// that check's 403 there and must be here (cmd/api).
	NotFound http.Handler
}

// Wrap puts the guard in front of the mux.
func (g Guard) Wrap(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch outcome, routed := g.Served.read(request); outcome {
		case sameRoute:
			forwarded := request.Clone(request.Context())
			// Path already holds the decoded path; RawPath is the hint that
			// decides how it is escaped again, and so what ServeMux -- and,
			// if the gate hands this request over, the Python tier -- is
			// shown. Both decode it back to the same path.
			forwarded.URL.RawPath = routed
			mux.ServeHTTP(response, forwarded)
		case noRoute:
			g.NotFound.ServeHTTP(response, request)
		case untouched:
			mux.ServeHTTP(response, request)
		}
	})
}

// NotFound is Starlette's bare 404 as a handler, carrying the request's id.
func NotFound() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		WriteRouterNotFound(response, requestcontext.IDFrom(request))
	})
}

// outcome is one of the guard's three (Guard, "Scope").
type outcome int

const (
	// untouched: the routers agree, or the path is not this tier's to settle.
	untouched outcome = iota
	// sameRoute: Starlette matches one of this tier's routes; run that one.
	sameRoute
	// noRoute: Starlette matches nothing where this tier would have answered.
	noRoute
)

// Served is the method-and-path pairs this tier answers, held twice: cut
// into segments, for the questions asked about a path as Starlette reads it,
// and in a ServeMux, for the one question only ServeMux can answer.
type Served struct {
	routes []template
	// mux is ServeMux's own reading of the same templates, asked which route
	// a path it would clean is redirected *towards*. Asking the real thing
	// is surer than a second hand-written copy of its cleaning and matching.
	mux *http.ServeMux
}

// template is one served route, cut into segments.
type template struct {
	method   string
	segments []segment
}

// segment is one literal, or a parameter (whose name the guard never needs).
type segment struct {
	literal   string
	parameter bool
}

// NewServed reads pairs written as cmd/api's servedRoutes() writes them:
// "GET /api/v1/folders/{folder_id}/breadcrumb".
//
// A template must be plain segments: a literal, or one `{name}` filling the
// whole segment. That is every route this tier has, and it is refused rather
// than tolerated when it is not, because the guard's 404 for an empty segment
// rests on no route here being able to match across one (Guard, "What it
// deliberately does not settle"). A `{name...}` wildcard or a trailing slash
// would break that silently.
func NewServed(pairs ...string) (Served, error) {
	served := Served{mux: http.NewServeMux()}
	seen := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		method, address, spaced := strings.Cut(pair, " ")
		if !spaced || method == "" || !strings.HasPrefix(address, "/") || len(address) < 2 {
			return Served{}, fmt.Errorf("served route %q is not \"METHOD /path\"", pair)
		}
		if seen[pair] {
			// ServeMux panics on a pattern registered twice; say it plainly.
			return Served{}, fmt.Errorf("served route %q is listed twice", pair)
		}
		seen[pair] = true

		route := template{method: method}
		for _, written := range strings.Split(address[1:], "/") {
			switch {
			case written == "":
				return Served{}, fmt.Errorf("served route %q has an empty segment or a trailing slash", pair)
			case strings.HasPrefix(written, "{") && strings.HasSuffix(written, "}"):
				name := written[1 : len(written)-1]
				if name == "" || name == "$" || strings.ContainsAny(name, "{}.:") {
					return Served{}, fmt.Errorf("served route %q has a parameter that is not one plain segment: %q", pair, written)
				}
				route.segments = append(route.segments, segment{parameter: true})
			case strings.ContainsAny(written, "{}"):
				return Served{}, fmt.Errorf("served route %q has a segment that is partly a parameter: %q", pair, written)
			default:
				route.segments = append(route.segments, segment{literal: written})
			}
		}
		served.routes = append(served.routes, route)
		served.mux.Handle(pair, http.NotFoundHandler())
	}
	return served, nil
}

// read decides one request's outcome. For sameRoute it also returns the
// escaped path to send on.
func (s Served) read(request *http.Request) (outcome, string) {
	// ServeMux does not clean a CONNECT request's path, and no route of
	// either tier takes one.
	if request.Method == http.MethodConnect {
		return untouched, ""
	}
	escaped := request.URL.EscapedPath()
	decoded := request.URL.Path

	// The two tests for "the routers agree". ServeMux cleans the *escaped*
	// path and redirects when cleaning changed it; and its segments are the
	// decoded path's segments unless an escaped slash is hiding in one.
	tidy := tidied(escaped) == escaped
	sameSegments := strings.Count(escaped, "/") == strings.Count(decoded, "/")
	if tidy && sameSegments {
		return untouched, ""
	}

	// Outcome 1: the Python tier would run one of this tier's routes.
	if s.starletteMatches(request.Method, decoded) {
		return sameRoute, escapedBySegment(decoded)
	}

	// Outcome 2 or 3. Whether the path, as it was written, is one of this
	// tier's routes with *something* -- or nothing -- where each parameter
	// goes.
	addressed := s.addressedTo(request.Method, escaped)

	if tidy {
		// Only an escaped slash is wrong with this path, and Starlette has
		// no route for it. ServeMux would run this tier's route with the
		// slash inside a parameter -- or, when a parameter is nothing *but*
		// an escaped slash, would take it for a trailing slash and answer
		// its own plain-text 404.
		//
		// Unless the slash is the *end* of the decoded path
		// (`/api/v1/pats/7%2F`). To Starlette that is a trailing slash, and
		// a trailing slash is redirect_slashes' to answer -- a 307, measured
		// -- which this guard does not settle.
		if addressed && !strings.HasSuffix(decoded, "/") {
			return noRoute, ""
		}
		return untouched, ""
	}

	// ServeMux would redirect. Only an empty segment makes the 404 certain
	// (Guard, "What it deliberately does not settle").
	if hasDotSegment(escaped) || !hasEmptySegment(escaped) {
		return untouched, ""
	}
	// The route ServeMux would redirect *towards*: the one the cleaned path
	// reaches. `/api/v1//pats` is not addressed to a route as written -- it
	// has a segment too many -- but its 301 would send the client on to one.
	_, towards := s.mux.Handler(request)
	if addressed || towards != "" {
		return noRoute, ""
	}
	return untouched, ""
}

// starletteMatches is whether Starlette's router would run one of this
// tier's routes for the decoded path: the same number of segments, each
// literal equal, each parameter `[^/]+` -- anything that is not empty,
// including "." and "..". The method is compared exactly; FastAPI adds no
// HEAD to a GET route.
func (s Served) starletteMatches(method, decoded string) bool {
	if !strings.HasPrefix(decoded, "/") {
		return false
	}
	given := strings.Split(decoded[1:], "/")
	for _, route := range s.routes {
		if route.method != method || len(route.segments) != len(given) {
			continue
		}
		matched := true
		for i, wanted := range route.segments {
			if given[i] == "" || (!wanted.parameter && given[i] != wanted.literal) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// addressedTo is whether the escaped path has one of this tier's routes'
// literals in their places and the same number of segments, **whatever** is
// where the parameters go -- including nothing at all, and including an
// escaped slash.
//
// It is the hand-written half of "would this tier have answered for one of
// its routes", and it exists for the shapes the real ServeMux cannot be
// asked about, because ServeMux does not dispatch them to the route they
// name:
//
//   - `POST /api/v1/prompts//copy`: the redirect's target,
//     `/api/v1/prompts/copy`, is no POST route of this tier, and yet the
//     request plainly names one and is answered a 301 by this tier.
//   - `POST /api/v1/prompts/%2F/copy`: ServeMux unescapes the segment to "/",
//     takes that for a trailing slash, and matches nothing (measured when
//     this guard was written).
//
// A literal is compared unescaped, as ServeMux compares it.
func (s Served) addressedTo(method, escaped string) bool {
	given := strings.Split(strings.TrimPrefix(escaped, "/"), "/")
	for _, route := range s.routes {
		if route.method != method || len(route.segments) != len(given) {
			continue
		}
		matched := true
		for i, wanted := range route.segments {
			if wanted.parameter {
				continue
			}
			literal, err := url.PathUnescape(given[i])
			if err != nil || literal != wanted.literal {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// tidied is net/http's unexported cleanPath, which is what ServeMux compares
// a request's escaped path with before it decides to redirect: path.Clean,
// with the trailing slash put back.
func tidied(escaped string) string {
	if escaped == "" {
		return "/"
	}
	if escaped[0] != '/' {
		escaped = "/" + escaped
	}
	cleaned := path.Clean(escaped)
	if escaped[len(escaped)-1] == '/' && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned
}

// hasDotSegment is whether the path has a literal "." or ".." segment, the
// two ServeMux resolves. "%2E" is not one: ServeMux cleans the escaped path,
// so an escaped dot is an ordinary segment to it, as it is to Starlette.
func hasDotSegment(escaped string) bool {
	for _, given := range strings.Split(escaped, "/") {
		if given == "." || given == ".." {
			return true
		}
	}
	return false
}

// hasEmptySegment is whether the path has an empty segment that is not just
// a run of trailing slashes. Starlette strips *every* trailing slash before
// it tries its redirect, so `/api/v1/pats//` is `redirect_slashes`' 307 and
// not the router's 404.
func hasEmptySegment(escaped string) bool {
	return strings.Contains(strings.TrimRight(escaped, "/"), "//")
}

// escapedBySegment writes a decoded path so that ServeMux sees exactly the
// segments Starlette sees: every slash in it is a separator, and a "." or
// ".." segment is escaped, because Starlette treats those as ordinary values
// and ServeMux would resolve a literal one and redirect.
func escapedBySegment(decoded string) string {
	given := strings.Split(decoded, "/")
	for i, part := range given {
		switch part {
		case ".":
			given[i] = "%2E"
		case "..":
			given[i] = "%2E%2E"
		default:
			given[i] = url.PathEscape(part)
		}
	}
	return strings.Join(given, "/")
}
