package accountprofile

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

// CookiePolicy is app/auth/session_cookies.py's `clear_session`, which is the
// one thing the delete route writes besides its rows.
//
// It is a mirror of Starlette's `Response.delete_cookie` and not of the idea of
// deleting a cookie, because the bytes a browser is handed decide whether the
// cookie goes:
//
//	surfsense_session=""; expires=Mon, 09 Nov 2026 00:46:14 GMT; HttpOnly; Max-Age=0; Path=/; SameSite=lax
//
// Four things in that line were taken off the running library rather than
// written from its documentation:
//
//   - **the value is `""` with the quotes.** `SimpleCookie` quotes an empty
//     value, so `key=` would be a different header.
//   - **`expires=0` renders as *now*, not as the epoch.** Starlette passes the
//     integer through to `Morsel`, whose `expires` for an int is
//     `_getdate(value)` — the current time plus that many seconds — so the date
//     is the request's own clock to the second, and two requests a second
//     apart carry two different headers. That is the one part of this header
//     neither tier can make equal, and it is masked in the parity suite the way
//     the error envelope's timestamp is.
//   - **the attribute order is the morsel's keys sorted**, not the order
//     Starlette sets them in and not the order `Morsel._reserved` lists them:
//     domain, expires, httponly, max-age, path, samesite, secure.
//   - **`expires=` is lower case and the rest are not.** That is `Morsel`'s own
//     table of printed names.
type CookiePolicy struct {
	// SessionName and RefreshName are SESSION_COOKIE_NAME and
	// REFRESH_COOKIE_NAME, cleared in that order because `clear_session`
	// iterates them in that order.
	SessionName string
	RefreshName string
	// Domain is COOKIE_DOMAIN, empty for a deployment that sets none — in
	// which case no Domain attribute is written at all, as `domain=None`
	// leaves it out.
	Domain string
	// SameSite is SESSION_COOKIE_SAMESITE, already lower-cased by that config
	// (and `none` is refused there, so it is `lax` or `strict` in practice).
	SameSite string
	// SecurePolicy is SESSION_COOKIE_SECURE_POLICY: `always`, `never`, or
	// `auto`, which reads the request.
	SecurePolicy string
}

// Clear writes the two Set-Cookie headers `clear_session` writes.
//
// forwardedProto is the request's `X-Forwarded-Proto`, which is all the
// `auto` policy reads here: this tier listens on plain HTTP behind the proxy
// and has no TLS configuration at all, so there is no second branch for a
// connection that arrived over TLS — the proxy's header is how it says so, and
// it is the header uvicorn's own scheme comes from in this deployment too.
func (p CookiePolicy) Clear(response http.ResponseWriter, forwardedProto string, now time.Time) {
	secure := p.secureFor(forwardedProto)
	for _, name := range []string{p.SessionName, p.RefreshName} {
		response.Header().Add("Set-Cookie", p.deletion(name, secure, now))
	}
}

// deletion is one header's bytes.
func (p CookiePolicy) deletion(name string, secure bool, now time.Time) string {
	attributes := map[string]string{
		// Morsel's own printed names, which is why the capitalisation is not
		// uniform.
		"expires":  "expires=" + now.UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		"httponly": "HttpOnly",
		"max-age":  "Max-Age=0",
		"path":     "Path=/",
	}
	if p.Domain != "" {
		attributes["domain"] = "Domain=" + p.Domain
	}
	if p.SameSite != "" {
		attributes["samesite"] = "SameSite=" + p.SameSite
	}
	if secure {
		attributes["secure"] = "Secure"
	}

	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	written := &strings.Builder{}
	written.WriteString(name)
	written.WriteString(`=""`)
	for _, key := range keys {
		written.WriteString("; ")
		written.WriteString(attributes[key])
	}
	return written.String()
}

// secureFor is `_cookie_secure(request)`.
//
// `auto` reads `X-Forwarded-Proto`'s **first** entry and falls back to the
// request's own scheme, which is `http` for every request that reaches this
// process. The last branch of that function — `BACKEND_URL` starting with
// https — is for a call with no request at all, which `clear_session` never
// makes from this route, so it is not mirrored and `BACKEND_URL` is not a
// setting this tier reads.
func (p CookiePolicy) secureFor(forwardedProto string) bool {
	switch p.SecurePolicy {
	case "always":
		return true
	case "never":
		return false
	}
	if forwardedProto == "" {
		return false
	}
	first := forwardedProto
	if comma := strings.Index(forwardedProto, ","); comma >= 0 {
		first = forwardedProto[:comma]
	}
	return strings.EqualFold(strings.TrimSpace(first), "https")
}
