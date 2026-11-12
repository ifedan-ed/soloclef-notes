package signin

import (
	"context"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// accessAudience is the audience internal/principal requires of an access
// token. The value is the one the Python tier's sign-in library wrote, kept
// so a token is one format across both tiers while both run.
const accessAudience = "fastapi-users:auth"

// mintAccess is a new access token for one sign-in of the account: HS256
// over the session secret, naming the account, the sign-in (`sid`, the
// refresh-token family), when it was issued (MIN_ISSUED_AT is checked
// against `iat`) and when it expires. It answers the expiry as a unix
// second, which is what both apps schedule their next refresh by.
//
// The sign-in is in the token so that ending the sign-in ends the token:
// the gate refuses a token whose family has no unspent token left
// (internal/principal, SessionIsLive). Without it a signed-out device kept
// working until its token's hour was up.
func mintAccess(secret, userID, sessionID string, now time.Time) (string, int64, error) {
	expires := now.Add(accessLifetime).Unix()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"sid": sessionID,
		"aud": accessAudience,
		"iat": now.Unix(),
		"exp": expires,
	})
	signed, err := token.SignedString([]byte(secret))
	return signed, expires, err
}

// MintSession signs the account in as a browser would be, with no password
// asked, and answers the sign-in's access token. It is exported for one
// caller: the end-to-end test routes (internal/e2eseed), which hand the
// suite a session without the sign-in limit. It records a real sign-in (a
// refresh-token family) because the gate accepts no access token without
// one, so the suite's tokens are exactly what a sign-in hands out and end
// the same ways.
func MintSession(ctx context.Context, pool *pgxpool.Pool, secret, userID string, now time.Time) (string, error) {
	started, err := startFamily(ctx, pool, userID, kindWeb, nil, now)
	if err != nil {
		return "", err
	}
	token, _, err := mintAccess(secret, userID, started.familyID, now)
	return token, err
}

// accessExpiry reads the expiry of an access token this tier already
// accepted (the gate verified it), for GET /auth/session.
func accessExpiry(secret, token string) (int64, bool) {
	claims := jwt.MapClaims{}
	parsed, err := jwt.NewParser(jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(accessAudience)).
		ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return []byte(secret), nil })
	if err != nil || !parsed.Valid {
		return 0, false
	}
	expires, err := claims.GetExpirationTime()
	if err != nil || expires == nil {
		return 0, false
	}
	return expires.Unix(), true
}
