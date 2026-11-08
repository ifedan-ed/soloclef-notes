package agentsignin

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// leeway is access_token.LEEWAY_SECONDS: the clock difference tolerated between
// this server and the provider.
const leeway = 60 * time.Second

// requiredClaims is the `options={"require": [...]}` the Python tier passes. A
// claim that is absent *or* null fails, as _validate_required_claims does.
var requiredClaims = []string{"exp", "iat", "iss", "aud", "sub"}

// checkClaims is PyJWT's _validate_claims for the options the Python tier
// passes, in its order and with its coercions.
//
// It is written out here rather than left to the JWT library's own validator
// because the two libraries disagree on the edges that matter to a token this
// tier must accept or refuse identically: PyJWT requires iat and reads it as
// Python's int() does, it treats exp at exactly the leeway boundary as expired,
// and it insists that sub and jti be strings.
func checkClaims(claims rawClaims, audience, issuer string, now time.Time) error {
	for _, name := range requiredClaims {
		if raw, present := claims[name]; !present || isNull(raw) {
			return fmt.Errorf("%w: token is missing the %s claim", ErrRefused, name)
		}
	}

	seconds := float64(now.UnixNano()) / float64(time.Second)
	margin := leeway.Seconds()

	if raw, present := claims["iat"]; present {
		issuedAt, err := pythonInt(raw)
		if err != nil {
			return fmt.Errorf("%w: issued-at claim (iat) must be an integer", ErrRefused)
		}
		if float64(issuedAt) > seconds+margin {
			return fmt.Errorf("%w: the token is not yet valid (iat)", ErrRefused)
		}
	}
	if raw, present := claims["nbf"]; present {
		notBefore, err := pythonInt(raw)
		if err != nil {
			return fmt.Errorf("%w: not-before claim (nbf) must be an integer", ErrRefused)
		}
		if float64(notBefore) > seconds+margin {
			return fmt.Errorf("%w: the token is not yet valid (nbf)", ErrRefused)
		}
	}
	if raw, present := claims["exp"]; present {
		expiresAt, err := pythonInt(raw)
		if err != nil {
			return fmt.Errorf("%w: expiry claim (exp) must be an integer", ErrRefused)
		}
		if float64(expiresAt) <= seconds-margin {
			return fmt.Errorf("%w: signature has expired", ErrRefused)
		}
	}

	if err := checkIssuer(claims["iss"], issuer); err != nil {
		return err
	}
	if err := checkAudience(claims["aud"], audience); err != nil {
		return err
	}
	if raw, present := claims["sub"]; present {
		if _, ok := jsonString(raw); !ok {
			return fmt.Errorf("%w: subject must be a string", ErrRefused)
		}
	}
	if raw, present := claims["jti"]; present {
		if _, ok := jsonString(raw); !ok {
			return fmt.Errorf("%w: JWT id must be a string", ErrRefused)
		}
	}
	return nil
}

// checkIssuer compares the issuer exactly, as PyJWT does. The trailing-slash
// tolerance of Settings.IsThisIssuer is only for deciding which verifier owns a
// token; what it is checked against is the issuer the provider itself publishes.
func checkIssuer(raw json.RawMessage, issuer string) error {
	claimed, ok := jsonString(raw)
	if !ok {
		return fmt.Errorf("%w: payload issuer (iss) must be a string", ErrRefused)
	}
	if claimed != issuer {
		return fmt.Errorf("%w: invalid issuer", ErrRefused)
	}
	return nil
}

// checkAudience is _validate_aud with strict mode off: one audience or a list
// of them, and the client must be among them.
func checkAudience(raw json.RawMessage, audience string) error {
	if !truthy(raw) {
		return fmt.Errorf("%w: token is missing the aud claim", ErrRefused)
	}
	claimed, err := audienceList(raw)
	if err != nil {
		return err
	}
	for _, candidate := range claimed {
		if candidate == audience {
			return nil
		}
	}
	return fmt.Errorf("%w: audience doesn't match", ErrRefused)
}

func audienceList(raw json.RawMessage) ([]string, error) {
	if single, ok := jsonString(raw); ok {
		return []string{single}, nil
	}
	var listed []json.RawMessage
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("%w: invalid aud claim format in token", ErrRefused)
	}
	audiences := make([]string, 0, len(listed))
	for _, entry := range listed {
		text, ok := jsonString(entry)
		if !ok {
			return nil, fmt.Errorf("%w: invalid aud claim format in token", ErrRefused)
		}
		audiences = append(audiences, text)
	}
	return audiences, nil
}

// pythonInt is what `int(value)` accepts for a claim PyJWT reads as a time: a
// JSON number, truncated towards zero, a boolean, or a string holding a whole
// number.
func pythonInt(raw json.RawMessage) (int64, error) {
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return 0, fmt.Errorf("not a whole number")
		}
		return int64(math.Trunc(number)), nil
	}
	var flag bool
	if err := json.Unmarshal(raw, &flag); err == nil {
		if flag {
			return 1, nil
		}
		return 0, nil
	}
	if text, ok := jsonString(raw); ok {
		value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("not a whole number")
		}
		return value, nil
	}
	return 0, fmt.Errorf("not a whole number")
}

func isNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}
