package agentsignin

import "strings"

// wellKnown is the discovery path settings.AgentSignInSettings.discovery_url
// appends, and the suffix ParseMetadata takes off again.
const wellKnown = "/.well-known/openid-configuration"

// Settings is what agent sign-in needs from this deployment, mirroring
// app/agent_sign_in/settings.py.
type Settings struct {
	// Issuer is exactly as the provider prints it, trailing slash included.
	Issuer string
	// ClientID is the provider's client id for the agent application. A token
	// is accepted only when its audience is this client, so a token the
	// provider issued to any other application — the web sign-in included — is
	// refused.
	ClientID string
	// ProviderName is the oauth_account.oauth_name the web sign-in links
	// identities under, so an identity linked there is found here.
	ProviderName string
}

// Enabled reports whether this deployment has an agent provider at all. When it
// does not, an agent's token is forwarded to the Python tier as before, which
// is also what refuses it if that tier has none either.
func (s Settings) Enabled() bool { return s.Issuer != "" && s.ClientID != "" }

// DiscoveryURL is where the provider publishes its endpoints and key set.
func (s Settings) DiscoveryURL() string {
	return strings.TrimRight(s.Issuer, "/") + wellKnown
}

// IsThisIssuer reports whether an issuer names this provider, comparing as
// AgentTokenVerifier.claims_this_issuer does: trailing slashes do not matter
// here, although the signature check below compares the issuer exactly.
func (s Settings) IsThisIssuer(issuer string) bool {
	return issuer != "" && strings.TrimRight(issuer, "/") == strings.TrimRight(s.Issuer, "/")
}
