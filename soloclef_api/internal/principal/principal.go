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
	// SessionID is the sign-in a session's access token names (its `sid`,
	// the refresh-token family the gate just found live); empty for an API
	// key or an agent. A route that acts on "every sign-in but this one"
	// (a password change, internal/signin) reads it here rather than parsing
	// the token a second time and perhaps reading a different one than the
	// gate accepted.
	SessionID string
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
