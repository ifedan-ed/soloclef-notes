package csrforigin

import "net/http"

// GuardingSessionWrites is the policy, also checking by origin the routes
// that make, renew or end the browser's session -- whether or not the
// request carries a session cookie.
//
// The cookie rule (Permits) exists because a cookie is ambient: a forged
// request carries it. A sign-in has no cookie to carry, so the rule never
// looked at one, and a page anywhere could post a stranger's address and
// password to the sign-in: the browser keeps the cookies the answer sets,
// and the person is from then on working in the stranger's account,
// uploading their own files into it (login CSRF). A refresh and a sign-out
// are carried by the refresh cookie, which the cookie rule does not look
// for either. These routes answer a foreign origin the same 403 a
// cookie-authenticated write gets.
//
// A request that names no origin at all (no Origin, no Referer) still
// passes: that is the phone app and other native clients, which hold their
// tokens themselves, and a browser cannot be made to send such a write
// cross-origin -- every browser names the origin of a cross-origin POST.
//
// The patterns are "METHOD /path", as the routes are registered.
func (p Policy) GuardingSessionWrites(patterns ...string) Policy {
	routes := http.NewServeMux()
	for _, pattern := range patterns {
		routes.Handle(pattern, http.NotFoundHandler())
	}
	p.sessionWrites = func(request *http.Request) bool {
		_, pattern := routes.Handler(request)
		return pattern != ""
	}
	return p
}

// writesSession reports whether this request is one of the session writes.
func (p Policy) writesSession(request *http.Request) bool {
	return p.sessionWrites != nil && p.sessionWrites(request)
}
