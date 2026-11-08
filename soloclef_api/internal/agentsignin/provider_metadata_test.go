package agentsignin

import (
	"slices"
	"testing"
)

const discoveryURL = "https://sso.example.invalid/application/o/agents/.well-known/openid-configuration"

func TestMetadataIsAcceptedOnlyForTheProviderAsked(t *testing.T) {
	cases := map[string]struct {
		document string
		accepted bool
	}{
		"the provider it was fetched from": {
			`{"issuer":"https://sso.example.invalid/application/o/agents/",
			  "authorization_endpoint":"https://sso.example.invalid/authorize",
			  "token_endpoint":"https://sso.example.invalid/token",
			  "jwks_uri":"https://sso.example.invalid/jwks"}`, true},
		"another provider entirely": {
			`{"issuer":"https://attacker.example.invalid/",
			  "authorization_endpoint":"https://sso.example.invalid/authorize",
			  "token_endpoint":"https://sso.example.invalid/token",
			  "jwks_uri":"https://sso.example.invalid/jwks"}`, false},
		"no jwks_uri": {
			`{"issuer":"https://sso.example.invalid/application/o/agents/",
			  "authorization_endpoint":"https://sso.example.invalid/authorize",
			  "token_endpoint":"https://sso.example.invalid/token"}`, false},
		"an empty jwks_uri": {
			`{"issuer":"https://sso.example.invalid/application/o/agents/",
			  "authorization_endpoint":"https://sso.example.invalid/authorize",
			  "token_endpoint":"https://sso.example.invalid/token","jwks_uri":""}`, false},
	}
	for name, test := range cases {
		_, err := ParseMetadata([]byte(test.document), discoveryURL)
		if test.accepted && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !test.accepted && err == nil {
			t.Errorf("%s: accepted, want refused", name)
		}
	}
}

// A provider that does not say which algorithms it signs with is assumed to
// sign with RS256, as discovery.parse_metadata assumes; a provider that says
// something unreadable is left with nothing, so no token can be accepted under
// it.
func TestSigningAlgorithmsMirrorTheirPythonFallbacks(t *testing.T) {
	cases := map[string][]string{
		`null`:              {"RS256"},
		`[]`:                {"RS256"},
		`""`:                {"RS256"},
		`["RS256","ES256"]`: {"RS256", "ES256"},
		`["HS256"]`:         {"HS256"},
		`"RS256"`:           {"R", "S", "2", "5", "6"},
	}
	for document, want := range cases {
		got, err := signingAlgorithms([]byte(document))
		if err != nil {
			t.Errorf("%s: %v", document, err)
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", document, got, want)
		}
	}
	if _, err := signingAlgorithms([]byte(`5`)); err == nil {
		t.Error("a number was read as a list of algorithms")
	}
}
