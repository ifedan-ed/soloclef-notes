package principal

import (
	"errors"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// tokenAudience is fastapi-users' JWTStrategy default, which the Python tier
// keeps (app/users.py: IatJWTStrategy over JWTStrategy).
const tokenAudience = "fastapi-users:auth"

// ErrForeignIssuer means the token is a JWT from somewhere other than this
// application: an identity provider's access token, which this tier does not
// verify. The Python tier's own access tokens carry no iss claim, so a token
// that has one is never one of them.
var ErrForeignIssuer = errors.New("token carries an issuer this tier does not verify")

// SessionToken reads a fastapi-users access token and returns the user id it
// names, applying the same checks the Python tier applies: HS256 against the
// shared secret, the audience, the expiry, and MIN_ISSUED_AT.
type SessionToken struct {
	Secret      string
	MinIssuedAt int64
}

func (s SessionToken) UserID(token string) (string, error) {
	if issuer, hasIssuer := unverifiedIssuer(token); hasIssuer {
		return "", fmt.Errorf("%w: %q", ErrForeignIssuer, issuer)
	}

	// PyJWT's own defaults, which fastapi-users relies on: the audience must be
	// present and match, exp and nbf are checked when present, and iat is not
	// required unless this deployment sets an epoch.
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithAudience(tokenAudience),
	)
	if _, err := parser.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return []byte(s.Secret), nil
	}); err != nil {
		return "", fmt.Errorf("token refused: %w", err)
	}

	if s.MinIssuedAt > 0 {
		issuedAt, err := claims.GetIssuedAt()
		if err != nil || issuedAt == nil || issuedAt.Unix() < s.MinIssuedAt {
			return "", errors.New("token was issued before this deployment's epoch")
		}
	}

	subject, err := claims.GetSubject()
	if err != nil || subject == "" {
		return "", errors.New("token names no subject")
	}
	userID, err := canonicalUUID(subject)
	if err != nil {
		return "", err
	}
	return userID, nil
}

// canonicalUUID accepts what Python's uuid.UUID() accepts — hyphenated, bare,
// braced or urn-prefixed — and returns the hyphenated form, so a token minted
// by the Python tier resolves to the same row here.
func canonicalUUID(value string) (string, error) {
	text := strings.TrimSpace(strings.ToLower(value))
	text = strings.TrimPrefix(text, "urn:uuid:")
	text = strings.TrimPrefix(text, "{")
	text = strings.TrimSuffix(text, "}")
	text = strings.ReplaceAll(text, "-", "")
	if len(text) != 32 {
		return "", fmt.Errorf("%q is not a user id", value)
	}
	for _, digit := range text {
		if !strings.ContainsRune("0123456789abcdef", digit) {
			return "", fmt.Errorf("%q is not a user id", value)
		}
	}
	return text[0:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:32], nil
}
