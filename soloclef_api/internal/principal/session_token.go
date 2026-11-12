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
// application: an identity provider's access token, which only the agent
// verifier may judge. This application's own access tokens carry no iss claim,
// so a token that has one is never one of them.
var ErrForeignIssuer = errors.New("token carries an issuer the session check does not verify")

// SessionToken reads this application's access token and returns the
// account and the sign-in it names: HS256 against the shared secret, the
// audience, the expiry, MIN_ISSUED_AT, and a sign-in id (`sid`).
type SessionToken struct {
	Secret      string
	MinIssuedAt int64
}

// Session is what an access token names: the account, and the sign-in it was
// minted for, which is the refresh-token family internal/signin started at
// the sign-in (refresh_tokens.family_id, the devices list's session_id).
//
// The sign-in is named so that ending it ends the token at once. The gate
// asks whether that sign-in is still live on every request (resolve.go,
// Directory.SessionIsLive), in the same statement that already asked whether
// the account is active, so the check costs no round trip of its own.
type Session struct {
	UserID    string
	SessionID string
}

// errNoSignIn is a token that names no sign-in: one the Python tier minted,
// or one this tier minted before 2026-11-12. Nothing can end such a token
// early, so it is refused, and the client's refresh (which both apps make on
// a 401) hands it one that does name its sign-in.
var errNoSignIn = errors.New("token names no sign-in")

// Read verifies the token and answers what it names. It reads no database:
// whether the sign-in is still live is the directory's question, asked by
// the resolver once the token has been verified.
func (s SessionToken) Read(token string) (Session, error) {
	if issuer, hasIssuer := unverifiedIssuer(token); hasIssuer {
		return Session{}, fmt.Errorf("%w: %q", ErrForeignIssuer, issuer)
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
		return Session{}, fmt.Errorf("token refused: %w", err)
	}

	if s.MinIssuedAt > 0 {
		issuedAt, err := claims.GetIssuedAt()
		if err != nil || issuedAt == nil || issuedAt.Unix() < s.MinIssuedAt {
			return Session{}, errors.New("token was issued before this deployment's epoch")
		}
	}

	subject, err := claims.GetSubject()
	if err != nil || subject == "" {
		return Session{}, errors.New("token names no subject")
	}
	userID, err := canonicalUUID(subject)
	if err != nil {
		return Session{}, err
	}
	// Canonical here, so the directory's ::uuid cast can never fail on a
	// signed token and turn a refusal into a 500.
	signIn, _ := claims["sid"].(string)
	if signIn == "" {
		return Session{}, errNoSignIn
	}
	sessionID, err := canonicalUUID(signIn)
	if err != nil {
		return Session{}, fmt.Errorf("%w: %v", errNoSignIn, err)
	}
	return Session{UserID: userID, SessionID: sessionID}, nil
}

// canonicalUUID accepts what Python's uuid.UUID() accepts — hyphenated, bare,
// braced or urn-prefixed — and returns the hyphenated form, so every spelling
// of one id names one row.
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
