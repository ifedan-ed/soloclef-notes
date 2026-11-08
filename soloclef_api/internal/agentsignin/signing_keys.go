package agentsignin

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// signingKey is one entry of the provider's key set.
//
// PublicKey is nil for a key the Python tier's reader accepts but this tier
// cannot verify with — a symmetric key, an Ed25519 key, a curve Go has no
// implementation of. Such a key is kept rather than dropped on purpose: the key
// a token is checked against is chosen by key id, and by position when the token
// names none, so dropping one here would hand a token to a *different* key than
// the Python tier hands it to. Keeping it means the token is refused, which is
// what the Python tier does with it too.
type signingKey struct {
	KeyID     string
	PublicKey crypto.PublicKey
}

// errNoUsableKeys is PyJWKSetError: a key set with nothing in it that can be
// read at all. The provider is unreadable rather than the token invalid.
var errNoUsableKeys = errors.New("the key set contains no usable keys")

// knownAlgorithms is PyJWT's get_default_algorithms with cryptography
// installed. A key whose alg is not one of them is skipped by the reader, so it
// is skipped here, because skipping changes which key a token is checked
// against.
var knownAlgorithms = map[string]bool{
	"none":  true,
	"HS256": true, "HS384": true, "HS512": true,
	"RS256": true, "RS384": true, "RS512": true,
	"ES256": true, "ES256K": true, "ES384": true, "ES512": true, "ES521": true,
	"PS256": true, "PS384": true, "PS512": true,
	"EdDSA": true,
}

// parseKeySet reads a JWKS document the way jwt.PyJWKSet.from_dict does: an
// empty set is an error, a key it cannot make sense of is skipped, and a set
// that is entirely unreadable is an error again.
func parseKeySet(document []byte) ([]signingKey, error) {
	var envelope struct {
		Keys json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(document, &envelope); err != nil {
		return nil, fmt.Errorf("the key set is not an object: %w", err)
	}
	var entries []map[string]json.RawMessage
	if len(envelope.Keys) > 0 {
		if err := json.Unmarshal(envelope.Keys, &entries); err != nil {
			// A key that is not an object is read by `.get` on a non-dict
			// there, which is not an error the Python tier handles; it answers
			// 500. This tier reports the provider unreadable instead, rather
			// than guess which key the token should have been checked against.
			return nil, fmt.Errorf("the key set is not a list of keys: %w", err)
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("the key set contains no keys")
	}

	keys := make([]signingKey, 0, len(entries))
	for _, entry := range entries {
		key, err := parseKey(entry)
		if err != nil {
			if errors.Is(err, errUnreadableByPython) {
				return nil, err
			}
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errNoUsableKeys
	}
	return keys, nil
}

// errUnreadableByPython marks the one key shape whose reader raises something
// the Python tier does not catch, so the request there ends as a 500. This tier
// treats the whole set as unreadable instead of carrying on with the rest, so
// the key a token is checked against can never differ between the two.
var errUnreadableByPython = errors.New("the key set cannot be read")

func parseKey(entry map[string]json.RawMessage) (signingKey, error) {
	keyType, _ := jsonString(entry["kty"])
	if keyType == "" {
		return signingKey{}, fmt.Errorf("the key names no kty")
	}

	algorithm, err := algorithmOf(entry, keyType)
	if err != nil {
		return signingKey{}, err
	}
	if algorithm == "none" {
		return signingKey{}, errUnreadableByPython
	}
	if !knownAlgorithms[algorithm] {
		return signingKey{}, fmt.Errorf("no algorithm named %q", algorithm)
	}

	keyID, _ := jsonString(entry["kid"])
	public, err := publicKeyOf(entry, keyType, algorithm)
	if err != nil {
		return signingKey{}, err
	}
	return signingKey{KeyID: keyID, PublicKey: public}, nil
}

// algorithmOf is PyJWK's own choice: the key's own alg when it has one, else
// the one its kty and crv imply.
func algorithmOf(entry map[string]json.RawMessage, keyType string) (string, error) {
	if raw, present := entry["alg"]; present && truthy(raw) {
		named, ok := jsonString(raw)
		if !ok {
			return "", fmt.Errorf("the key's alg is not a string")
		}
		return named, nil
	}
	curve, _ := jsonString(entry["crv"])
	switch keyType {
	case "EC":
		switch curve {
		case "P-256", "":
			return "ES256", nil
		case "P-384":
			return "ES384", nil
		case "P-521":
			return "ES512", nil
		case "secp256k1":
			return "ES256K", nil
		}
		return "", fmt.Errorf("unsupported crv %q", curve)
	case "RSA":
		return "RS256", nil
	case "oct":
		return "HS256", nil
	case "OKP":
		if curve == "" {
			return "", fmt.Errorf("the key names no crv")
		}
		if curve == "Ed25519" {
			return "EdDSA", nil
		}
		return "", fmt.Errorf("unsupported crv %q", curve)
	}
	return "", fmt.Errorf("unsupported kty %q", keyType)
}

// publicKeyOf is the Algorithm.from_jwk of the algorithm chosen above: the key
// type it insists on, and the members it needs. A key it accepts that this tier
// cannot verify with comes back nil.
func publicKeyOf(entry map[string]json.RawMessage, keyType, algorithm string) (crypto.PublicKey, error) {
	switch algorithm[0:2] {
	case "HS":
		if keyType != "oct" {
			return nil, fmt.Errorf("not an HMAC key")
		}
		if _, present := entry["k"]; !present {
			return nil, fmt.Errorf("the HMAC key has no k")
		}
		return nil, nil
	case "RS", "PS":
		if keyType != "RSA" {
			return nil, fmt.Errorf("not an RSA key")
		}
		return rsaPublicKey(entry)
	case "ES":
		if keyType != "EC" {
			return nil, fmt.Errorf("not an elliptic curve key")
		}
		return ecdsaPublicKey(entry)
	case "Ed":
		if keyType != "OKP" {
			return nil, fmt.Errorf("not an octet key pair")
		}
		if _, present := entry["x"]; !present {
			return nil, fmt.Errorf("the octet key pair has no x")
		}
		return nil, nil
	}
	return nil, fmt.Errorf("no algorithm named %q", algorithm)
}

func rsaPublicKey(entry map[string]json.RawMessage) (crypto.PublicKey, error) {
	modulus, modulusOK := base64URLUint(entry["n"])
	exponent, exponentOK := base64URLUint(entry["e"])
	if !modulusOK || !exponentOK {
		return nil, fmt.Errorf("not a public or private RSA key")
	}
	if !exponent.IsInt64() || exponent.Int64() <= 0 {
		return nil, fmt.Errorf("the RSA key's exponent is unusable")
	}
	return &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}, nil
}

// ecdsaCurves are the curves ECAlgorithm.from_jwk names, with the coordinate
// length it insists on. secp256k1 is read there and has no implementation here.
var ecdsaCurves = map[string]struct {
	curve  elliptic.Curve
	length int
}{
	"P-256":     {elliptic.P256(), 32},
	"P-384":     {elliptic.P384(), 48},
	"P-521":     {elliptic.P521(), 66},
	"secp256k1": {nil, 32},
}

func ecdsaPublicKey(entry map[string]json.RawMessage) (crypto.PublicKey, error) {
	x, xOK := base64URLBytes(entry["x"])
	y, yOK := base64URLBytes(entry["y"])
	if !xOK || !yOK {
		return nil, fmt.Errorf("not an elliptic curve key")
	}
	name, _ := jsonString(entry["crv"])
	shape, known := ecdsaCurves[name]
	if !known {
		return nil, fmt.Errorf("unsupported crv %q", name)
	}
	if len(x) != shape.length || len(y) != shape.length {
		return nil, fmt.Errorf("coordinates are not %d bytes for curve %s", shape.length, name)
	}
	if shape.curve == nil {
		return nil, nil
	}
	return &ecdsa.PublicKey{
		Curve: shape.curve,
		X:     new(big.Int).SetBytes(x),
		Y:     new(big.Int).SetBytes(y),
	}, nil
}

func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	return text, true
}

func base64URLBytes(raw json.RawMessage) ([]byte, bool) {
	text, ok := jsonString(raw)
	if !ok {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		return nil, false
	}
	return decoded, true
}

func base64URLUint(raw json.RawMessage) (*big.Int, bool) {
	decoded, ok := base64URLBytes(raw)
	if !ok {
		return nil, false
	}
	return new(big.Int).SetBytes(decoded), true
}
