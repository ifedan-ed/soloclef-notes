package agentsignin

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"testing"
)

// publicJWK renders a freshly made key as the provider would publish it. No key
// in this repository is a real one: this repository has a public copy.
func publicJWK(t *testing.T, keyID string, key *rsa.PublicKey) map[string]any {
	t.Helper()
	return map[string]any{
		"kty": "RSA",
		"kid": keyID,
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
	}
}

func keySet(t *testing.T, keys ...map[string]any) []byte {
	t.Helper()
	document, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatalf("marshalling the key set: %v", err)
	}
	return document
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("making a key: %v", err)
	}
	return key
}

func TestTheKeyChosenIsTheOneTheTokenNames(t *testing.T) {
	first, second := newRSAKey(t), newRSAKey(t)
	document := keySet(t,
		publicJWK(t, "first", &first.PublicKey),
		publicJWK(t, "second", &second.PublicKey))

	named := "second"
	chosen, err := keyFor(document, &named)
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	if chosen == nil || chosen.KeyID != "second" {
		t.Fatalf("got %v, want the key named second", chosen)
	}

	// A token naming no key id is checked against the first key in the set,
	// which is what jwt._key_for returns for kid=None.
	chosen, err = keyFor(document, nil)
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	if chosen == nil || chosen.KeyID != "first" {
		t.Fatalf("got %v, want the first key", chosen)
	}

	missing := "rotated-away"
	chosen, err = keyFor(document, &missing)
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	if chosen != nil {
		t.Fatalf("got %v, want no key", chosen)
	}
}

// A key this tier cannot verify with must still occupy its place in the set,
// or a token naming no key id would be checked against a *different* key here
// than in the Python tier — where jwt.PyJWKSet keeps such a key and the
// verification then fails.
func TestAKeyThisTierCannotUseKeepsItsPlace(t *testing.T) {
	usable := newRSAKey(t)
	document := keySet(t,
		map[string]any{"kty": "oct", "kid": "shared", "k": base64.RawURLEncoding.EncodeToString([]byte("secret"))},
		publicJWK(t, "signing", &usable.PublicKey))

	chosen, err := keyFor(document, nil)
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	if chosen == nil || chosen.KeyID != "shared" {
		t.Fatalf("got %v, want the symmetric key in first place", chosen)
	}
	if chosen.PublicKey != nil {
		t.Error("a symmetric key was offered as a verification key")
	}
}

func TestAKeySetWithNothingReadableIsTheProvidersProblem(t *testing.T) {
	for name, document := range map[string]string{
		"no keys at all":     `{"keys":[]}`,
		"no keys member":     `{}`,
		"nothing usable":     `{"keys":[{"kty":"RSA"},{"crv":"P-256","kty":"EC"}]}`,
		"not a list of keys": `{"keys":["abc"]}`,
	} {
		if _, err := parseKeySet([]byte(document)); err == nil {
			t.Errorf("%s: read as a key set", name)
		}
	}
}

// A key the reader skips must be skipped here too, for the same reason: it
// changes which key is first.
func TestUnreadableKeysAreSkippedNotKept(t *testing.T) {
	usable := newRSAKey(t)
	document := keySet(t,
		map[string]any{"kid": "no-kty"},
		map[string]any{"kty": "EC", "kid": "bad-curve", "crv": "P-192", "x": "AA", "y": "AA"},
		publicJWK(t, "signing", &usable.PublicKey))

	keys, err := parseKeySet(document)
	if err != nil {
		t.Fatalf("parseKeySet: %v", err)
	}
	if len(keys) != 1 || keys[0].KeyID != "signing" {
		t.Fatalf("got %v, want only the signing key", keys)
	}
}
