// Package agentsignin verifies an AI agent's access token from the identity
// provider and finds the account it acts as, as app/agent_sign_in does.
//
// An agent (claude.ai, ChatGPT) signs in at the identity provider and sends the
// access token it got. This package is the only thing that verifies such a
// token, so every rule the Python version had is kept rather than reinvented:
// the accepted algorithms, the leeway, the required claims, the audience and
// issuer checks, the key-rotation refresh and its cooldown, the access claim,
// and which account an identity maps to. A difference in any of them would
// have locked existing agents out, or let a token in that was refused before.
//
// The division of the files follows app/agent_sign_in's own: the token
// (access_token.py), the account (account.py) and the HTTP verdict
// (principal.py) are three separate decisions, and a difference in any of them
// is either a lockout or a bypass.
package agentsignin

import "errors"

// ErrRefused is access_token.AgentTokenRefusedError: the token was read and is
// not a valid agent sign-in. The reason belongs in the log, never in the body.
var ErrRefused = errors.New("the token is not a valid agent sign-in")

// ErrProviderUnavailable is access_token.AgentProviderUnavailableError: the
// provider's metadata or keys could not be read, so the token was not judged
// either way. It is answered with 503 and never 401, because a 401 would make
// every connected agent drop a token that may be perfectly good and send its
// owner back through a sign-in.
var ErrProviderUnavailable = errors.New("the identity provider could not be read")

// ErrNoAccount is account.NoAccountForAgentError: a genuine sign-in for which
// there is no usable account. An agent sign-in never creates one.
var ErrNoAccount = errors.New("no account matches this sign-in")

// Identity is what a verified token says about the person behind the agent,
// mirroring access_token.AgentIdentity.
type Identity struct {
	Subject string
	// Email is lowercased and trimmed, as the Python tier stores it, and empty
	// when the token carries none.
	Email string
	// EmailVerified is true only when the claim is the JSON true.
	EmailVerified bool
	// NotesAccess is the token's access claim: whether the provider still lets
	// this person into the application. Nil when the token carries no such
	// claim or it is not a JSON boolean, which is refused exactly as false is.
	NotesAccess *bool
}
