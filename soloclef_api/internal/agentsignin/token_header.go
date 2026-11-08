package agentsignin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// tokenHeader is the JOSE header, read without verifying anything, as
// jwt.get_unverified_header does.
type tokenHeader struct {
	Algorithm string
	// KeyID is the kid the token names, absent when it names none.
	KeyID *string
}

// readHeader mirrors PyJWS.get_unverified_header: three segments, a header that
// is a JSON object, and a kid that is a string if it is there at all. It does
// not insist on an alg — a token without one is refused by the algorithm check,
// in those words, exactly as it is there.
func readHeader(token string) (tokenHeader, error) {
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		return tokenHeader{}, fmt.Errorf("not enough segments")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(segments[0])
	if err != nil {
		return tokenHeader{}, fmt.Errorf("invalid header padding: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(decoded, &fields); err != nil {
		return tokenHeader{}, fmt.Errorf("invalid header string: %w", err)
	}

	header := tokenHeader{}
	if algorithm, ok := jsonString(fields["alg"]); ok {
		header.Algorithm = algorithm
	}
	if raw, present := fields["kid"]; present {
		keyID, ok := jsonString(raw)
		if !ok {
			return tokenHeader{}, fmt.Errorf("key ID header parameter must be a string")
		}
		header.KeyID = &keyID
	}
	return header, nil
}

// readPayload is the claim set, read without verifying anything, as
// jwt.decode(options={"verify_signature": False}) does.
func readPayload(token string) (map[string]json.RawMessage, error) {
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		return nil, fmt.Errorf("not enough segments")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return nil, fmt.Errorf("invalid payload padding: %w", err)
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, fmt.Errorf("invalid payload string: %w", err)
	}
	return claims, nil
}
