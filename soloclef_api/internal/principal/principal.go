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
)

// Principal is the resolved caller.
type Principal struct {
	UserID string
	Method Method
}

// IsGated reports whether this principal only sees workspaces that have API
// access switched on, matching AuthContext.is_gated.
func (p Principal) IsGated() bool {
	return p.Method == MethodPersonalAccessToken
}
