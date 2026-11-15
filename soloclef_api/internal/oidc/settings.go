package oidc

import "strings"

// wellKnown is the discovery path appended to the issuer, and the suffix
// ParseMetadata takes off again to compare the issuer with its URL.
const wellKnown = "/.well-known/openid-configuration"

// Settings names the provider and this application's client there.
type Settings struct {
	// Issuer is exactly as the provider prints it, trailing slash included.
	Issuer string
	// ClientID is this application's client id at the provider. A token is
	// accepted only when its audience is this client, so a token the
	// provider issued to any other application is refused.
	ClientID string
}

// DiscoveryURL is where the provider publishes its endpoints and key set.
func (s Settings) DiscoveryURL() string {
	return strings.TrimRight(s.Issuer, "/") + wellKnown
}

// NewVerifier is the verifier for a provider, by its issuer exactly as the
// provider prints it. The discovery document is read from under the issuer
// and must name that issuer back, so a mistyped address is a refused
// document and never somebody else's keys.
func NewVerifier(issuer, clientID string) *Verifier {
	settings := Settings{Issuer: issuer, ClientID: clientID}
	return &Verifier{
		Settings:  settings,
		Directory: &ProviderDirectory{DiscoveryURL: settings.DiscoveryURL()},
	}
}
