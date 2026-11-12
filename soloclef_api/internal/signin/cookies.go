package signin

import (
	"net/http"
	"strings"
	"time"
)

// CookiePolicy is how the web app's two session cookies are written: the
// access token and the refresh token, both HttpOnly so no script on the page
// can read either.
type CookiePolicy struct {
	// SessionName holds the access token; the gate reads it
	// (internal/principal). RefreshName holds the refresh token, which only
	// the refresh and the sign-out read.
	SessionName string
	RefreshName string
	// Domain is the cookies' Domain attribute; empty writes none, so they
	// belong to the API's own host.
	Domain string
	// SameSite is "lax" or "strict".
	SameSite string
	// SecurePolicy is "always", "never", or "auto", which marks the cookies
	// Secure when the proxy says the request came over HTTPS.
	SecurePolicy string
}

// write sets both cookies, each to expire when its token does.
func (p CookiePolicy) write(response http.ResponseWriter, request *http.Request, access, refresh string, now time.Time) {
	secure := p.secure(request)
	http.SetCookie(response, p.cookie(p.SessionName, access, accessLifetime, secure, now))
	http.SetCookie(response, p.cookie(p.RefreshName, refresh, webIdle, secure, now))
}

// writeAccess sets only the access cookie: the grace case of a refresh, in
// which the browser keeps the refresh cookie it has.
func (p CookiePolicy) writeAccess(response http.ResponseWriter, request *http.Request, access string, now time.Time) {
	http.SetCookie(response, p.cookie(p.SessionName, access, accessLifetime, p.secure(request), now))
}

// clear removes both cookies.
func (p CookiePolicy) clear(response http.ResponseWriter, request *http.Request, now time.Time) {
	secure := p.secure(request)
	for _, name := range []string{p.SessionName, p.RefreshName} {
		cookie := p.cookie(name, "", 0, secure, now)
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(0, 0)
		http.SetCookie(response, cookie)
	}
}

func (p CookiePolicy) cookie(name, value string, lifetime time.Duration, secure bool, now time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Domain:   p.Domain,
		MaxAge:   int(lifetime / time.Second),
		Expires:  now.Add(lifetime).UTC(),
		HttpOnly: true,
		Secure:   secure,
		SameSite: p.sameSite(),
	}
}

func (p CookiePolicy) sameSite() http.SameSite {
	if p.SameSite == "strict" {
		return http.SameSiteStrictMode
	}
	return http.SameSiteLaxMode
}

// secure reads the proxy's X-Forwarded-Proto (its first entry): this process
// listens on plain HTTP behind the proxy, so the header is the only place
// that knows how the browser connected.
func (p CookiePolicy) secure(request *http.Request) bool {
	switch p.SecurePolicy {
	case "always":
		return true
	case "never":
		return false
	}
	first, _, _ := strings.Cut(request.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(first), "https")
}
