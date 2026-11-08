package agentsignin

import (
	"encoding/json"
	"strings"
)

// NotesAccessClaim is access_token.NOTES_ACCESS_CLAIM: whether the provider
// lets this person open the application, decided by the provider's own bindings
// at the moment the token was made. The provider makes a new token at every
// connect and every refresh, and its refresh grant never re-checks an
// application's access policy itself, so this claim is how taking someone's
// access away reaches an agent that is already connected.
const NotesAccessClaim = "soloclef_notes_access"

// identityFrom reads the claims the account lookup needs, as
// AgentTokenVerifier.verify's return does.
func identityFrom(claims rawClaims) Identity {
	identity := Identity{Subject: claimText(claims["sub"])}
	identity.Email = strings.ToLower(strings.TrimSpace(claimText(claims["email"])))

	var verified bool
	if err := json.Unmarshal(claims["email_verified"], &verified); err == nil {
		identity.EmailVerified = verified
	}
	var access bool
	if err := json.Unmarshal(claims[NotesAccessClaim], &access); err == nil {
		identity.NotesAccess = &access
	}
	return identity
}

// claimText is `str(claims.get(name) or "")`: a string claim as it stands, and
// nothing for a claim that is absent, null or otherwise falsy. A claim of some
// other type is rendered as the JSON it was, which is not what Python's str()
// would print — and which, like Python's, cannot name a real account.
func claimText(raw json.RawMessage) string {
	if !truthy(raw) {
		return ""
	}
	if text, ok := jsonString(raw); ok {
		return text
	}
	return string(raw)
}
