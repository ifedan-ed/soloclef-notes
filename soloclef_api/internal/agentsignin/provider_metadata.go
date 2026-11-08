package agentsignin

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ProviderMetadata is the part of the discovery document this tier reads,
// mirroring app/auth_oidc/discovery.py's ProviderMetadata.
type ProviderMetadata struct {
	Issuer string
	// JWKSURI is where the signing keys are published.
	JWKSURI string
	// SigningAlgorithms is id_token_signing_alg_values_supported, which the
	// accepted algorithms are intersected with: a provider that no longer
	// signs with an algorithm cannot have a token accepted under it.
	SigningAlgorithms []string
}

// requiredFields are discovery.parse_metadata's: a document without them
// describes no provider this tier can use.
var requiredFields = []string{"issuer", "authorization_endpoint", "token_endpoint", "jwks_uri"}

// ParseMetadata accepts a discovery document only if it describes the provider
// asked for, as discovery.parse_metadata does: OpenID Connect Discovery 1.0
// section 4.3, the issuer must be the URL the document was fetched from minus
// the well-known suffix. A mismatch means the URL points somewhere other than
// the provider whose keys sign the tokens.
func ParseMetadata(document []byte, discoveryURL string) (ProviderMetadata, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		return ProviderMetadata{}, fmt.Errorf("discovery document is not an object: %w", err)
	}

	var missing []string
	for _, name := range requiredFields {
		if !truthy(fields[name]) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return ProviderMetadata{}, fmt.Errorf("discovery document lacks %s", strings.Join(missing, ", "))
	}

	var issuer string
	if err := json.Unmarshal(fields["issuer"], &issuer); err != nil {
		return ProviderMetadata{}, fmt.Errorf("discovery document's issuer is not a string")
	}
	expected := strings.TrimRight(strings.TrimSuffix(discoveryURL, wellKnown), "/")
	if strings.TrimRight(issuer, "/") != expected {
		return ProviderMetadata{}, fmt.Errorf("discovery issuer does not match its URL")
	}

	algorithms, err := signingAlgorithms(fields["id_token_signing_alg_values_supported"])
	if err != nil {
		return ProviderMetadata{}, err
	}
	var jwksURI string
	_ = json.Unmarshal(fields["jwks_uri"], &jwksURI)
	return ProviderMetadata{Issuer: issuer, JWKSURI: jwksURI, SigningAlgorithms: algorithms}, nil
}

// signingAlgorithms reproduces `tuple(document.get(...) or ("RS256",))`,
// including what it does with a value that is not a list: a JSON string becomes
// its characters, so no real algorithm is left and every token is refused.
func signingAlgorithms(raw json.RawMessage) ([]string, error) {
	if !truthy(raw) {
		return []string{"RS256"}, nil
	}
	var listed []string
	if err := json.Unmarshal(raw, &listed); err == nil {
		return listed, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.Split(text, ""), nil
	}
	return nil, fmt.Errorf("discovery document's id_token_signing_alg_values_supported is not a list")
}

// truthy is Python's truth test for a JSON value, which is what
// parse_metadata's own `not document.get(key)` applies: a missing key, null,
// false, zero, an empty string and an empty list or object are all absent.
func truthy(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case float64:
		return typed != 0
	case string:
		return typed != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	}
	return true
}
