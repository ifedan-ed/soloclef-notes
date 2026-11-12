// Package principal answers "who is making this request" with the same rules as
// the Python tier's app.users.get_auth_context.
//
// A route served here must accept exactly the credentials the Python route
// accepted and refuse exactly the ones it refused. Anything looser is an auth
// bypass; anything stricter is an outage for whoever holds that credential.
package principal

// Method is how the request proved who it is. It mirrors app.auth.context's
// AuthMethod, less the methods this tier does not verify itself.
type Method string

const (
	// MethodSession is the interactive session's HS256 access token, as a
	// bearer header or as the session cookie.
	MethodSession Method = "session"
	// MethodPersonalAccessToken is an ss_pat_ API key.
	MethodPersonalAccessToken Method = "pat"
	// MethodAgent is an AI agent's access token from the identity provider,
	// verified by internal/agentsignin.
	MethodAgent Method = "agent"
)

// Principal is the resolved caller.
type Principal struct {
	UserID string
	Method Method
}

// IsGated reports whether the caller is a program acting for the account (an
// API key or an agent's token) and not the person in a session. Such a caller
// is let in only while the account's API-access switch is on; the resolver
// checks that once, so no route does (resolve.go, ErrAPIAccessOff).
func (p Principal) IsGated() bool {
	return p.Method == MethodPersonalAccessToken || p.Method == MethodAgent
}

// IsSession reports whether an interactive session proved this request, matching
// AuthContext.is_session. The routes behind require_session_context refuse
// everything else, so an API key is told so rather than let through.
func (p Principal) IsSession() bool {
	return p.Method == MethodSession
}
