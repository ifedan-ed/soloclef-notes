package principal

import (
	"encoding/json"
	"strings"
)

// unverifiedIssuer reads a bearer token's iss claim without checking anything.
// Reading a claim off an unverified token is only ever used to decide which
// tier can verify it.
//
// It mirrors `jwt.decode(token, options={"verify_signature": False})` and the
// header validation behind it, because that is the call
// app.agent_sign_in.principal.is_agent_token makes, and the two tiers have to
// agree on *which* credential a request carries before they can agree on the
// answer. Every check PyJWS._load makes is here: three segments, a header and a
// payload that are JSON objects, a signature segment that is readable base64,
// and a kid that is a string if it is there at all. A token this says nothing
// about takes the session path, where it fails and ends as the same 401 — which
// is what happens there too.
func unverifiedIssuer(token string) (string, bool) {
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		return "", false
	}

	header, ok := decodeSegment(segments[0])
	if !ok {
		return "", false
	}
	if raw, present := header["kid"]; present {
		var keyID string
		if err := json.Unmarshal(raw, &keyID); err != nil {
			// PyJWS._validate_headers: "Key ID header parameter must be a
			// string". The token is then not readable at all, so it is not an
			// agent's either.
			return "", false
		}
	}
	// _load reads the signature segment as well, and a token it cannot read
	// there is a token it cannot read at all.
	if _, err := decodePythonBase64URL(segments[2]); err != nil {
		return "", false
	}

	claims, ok := decodeSegment(segments[1])
	if !ok {
		return "", false
	}
	var issuer string
	if err := json.Unmarshal(claims["iss"], &issuer); err != nil || issuer == "" {
		return "", false
	}
	return issuer, true
}

// decodeSegment reads one base64url segment as a JSON object.
func decodeSegment(segment string) (map[string]json.RawMessage, bool) {
	decoded, err := decodePythonBase64URL(segment)
	if err != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(decoded, &fields); err != nil {
		return nil, false
	}
	return fields, true
}
