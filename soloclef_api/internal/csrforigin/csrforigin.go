// Package csrforigin refuses a cookie-authenticated write from an origin this
// deployment does not own, as app/auth/csrf.py's CsrfOriginMiddleware does.
//
// It did not exist until this tier answered its first write, and nothing was
// missing before: the check only looks at POST, PUT, PATCH and DELETE, and
// every route handed over until now was a GET. The moment one write moved, a
// cross-site page could have made the browser send the session cookie to it and
// have the row written — which the Python tier refuses and this tier would not
// have. It was found by asking both tiers to revoke a key with the session
// cookie: 403 there, 204 here, and the key gone.
//
// Why only a cookie: a bearer credential is not ambient. A browser does not
// attach an Authorization header to a cross-site request on its own, so a
// request carrying one cannot have been forged, and the Python middleware
// returns early for it — before it even looks at whether the cookie is there.
// That early return is reproduced rather than tidied: a request with *both* an
// Authorization header and the session cookie is not checked, on either tier.
package csrforigin

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// unsafeMethods is app/auth/csrf.py's UNSAFE_METHODS.
var unsafeMethods = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// Refusal is the body the middleware writes. It is deliberately *not* the error
// envelope every route's refusal wears: this check answers from a middleware
// rather than by raising, so no exception handler wraps it, and what reaches a
// client is Starlette's own two-field JSONResponse.
const Refusal = `{"detail":"CSRF origin check failed"}`

// Policy is the set of origins a cookie-authenticated write may come from.
type Policy struct {
	// Allowed is _allowed_origins(): the configured list, plus the origin of
	// this deployment's own two addresses.
	Allowed map[string]bool
	// CookieName is SESSION_COOKIE_NAME; the check applies only when the
	// request carries that cookie.
	CookieName string
}

// NewPolicy builds the set the way _allowed_origins() builds it: the configured
// origins as written, and the scheme-and-authority of each address that names
// one.
func NewPolicy(cookieName string, configured []string, addresses ...string) Policy {
	allowed := make(map[string]bool, len(configured)+len(addresses))
	for _, origin := range configured {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			allowed[trimmed] = true
		}
	}
	for _, address := range addresses {
		if origin, named := originOf(address); named {
			allowed[origin] = true
		}
	}
	return Policy{Allowed: allowed, CookieName: cookieName}
}

// Middleware answers a forged-looking write and passes everything else on.
func (p Policy) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if p.Permits(request) {
			next.ServeHTTP(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Content-Length", strconv.Itoa(len(Refusal)))
		response.WriteHeader(http.StatusForbidden)
		_, _ = response.Write([]byte(Refusal))
	})
}

// Permits is the dispatch above, decision for decision.
func (p Policy) Permits(request *http.Request) bool {
	if !unsafeMethods[request.Method] {
		return true
	}
	if request.Header.Get("Authorization") != "" {
		return true
	}
	if _, carried := cookie(request, p.CookieName); !carried {
		return true
	}

	origin := request.Header.Get("Origin")
	if origin == "" {
		// Referer is read as a whole URL and reduced to its origin, so a
		// Referer naming a path still names an origin.
		if derived, named := originOf(request.Header.Get("Referer")); named {
			origin = derived
		}
	}

	// A deployment at an address nobody configured still works from its own
	// pages, which is why same-origin is allowed before the list is consulted.
	// The comparison is the Python one: the origin's authority against the Host
	// header, not against this process's idea of where it is.
	if sameOrigin(origin, request.Host) {
		return true
	}
	// An absent origin is refused here, because `None not in origins` is true:
	// a cookie-authenticated write that names no origin at all does not pass.
	return p.Allowed[origin]
}

// originOf is _origin_from_url: the scheme and authority of a URL that has
// both, and nothing for one that does not.
func originOf(address string) (string, bool) {
	if address == "" {
		return "", false
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	return parsed.Scheme + "://" + parsed.Host, true
}

// sameOrigin is _is_same_origin: the origin's authority against the Host
// header. An origin that is not a URL has no authority and matches nothing,
// which is urlparse's behaviour for the same text.
func sameOrigin(origin, host string) bool {
	if origin == "" || host == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return parsed.Host == host
}

func cookie(request *http.Request, name string) (string, bool) {
	if name == "" {
		return "", false
	}
	found, err := request.Cookie(name)
	if err != nil {
		return "", false
	}
	return found.Value, true
}
