package principal

// Refusal is a verdict the gate reached: the credential was checked and is not
// acceptable, or could not be checked, in a status and words of its own.
//
// It is distinct from ErrUnauthorized, which is the end of the resolution with
// nothing identified.
type Refusal struct {
	Status  int
	Message string
}

func (r Refusal) Error() string { return r.Message }

// ErrAPIAccessOff refuses an API key whose account has not switched API
// access on ("user".api_access_enabled, off for a new account).
//
// It is the one access control an account has: a key works only while the
// person who made it keeps the switch on. The refusal is a 403 and not a
// 401 on purpose: the credential is valid, and signing in again would not
// help. The person turns
// the switch on in a browser session (PATCH /api/v1/users/me/settings).
var ErrAPIAccessOff = Refusal{
	Status:  403,
	Message: "API access is not enabled for this account.",
}
