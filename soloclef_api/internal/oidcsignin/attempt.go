package oidcsignin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// The secrets one sign-in carries from its start to its callback.
//
// `state` goes to the provider and comes back in the callback's address;
// `nonce` goes into the ID token; the PKCE verifier never leaves this tier
// until the code is exchanged. All three ride in one short-lived, signed,
// HttpOnly cookie on the browser that started the sign-in, so a callback
// opened in any other browser (a forged or replayed link) has no cookie to
// match and is refused. Nothing is stored on the server, so any replica can
// finish a sign-in another one started.

const (
	attemptCookieName = "soloclef_oidc_sign_in"
	// attemptLifetime is how long a person has between pressing the button
	// and coming back from the provider (typing a mailed code included)
	// before the sign-in must be started again.
	attemptLifetime = 10 * time.Minute
	// attemptAudience keeps the sealed attempt from ever being taken for an
	// access token or a reset token, which the same secret signs.
	attemptAudience = "soloclef:oidc-sign-in"
)

type attempt struct {
	state    string
	nonce    string
	verifier string
	// returnTo is the page to land on, already made safe (return_path.go).
	returnTo string
	// app is the phone app's request, for a sign-in the app started
	// (app_sign_in.go); nil for the website's. The callback ends an app's
	// sign-in with a one-time code for the app instead of cookies.
	app *appRequest
}

// random is n random bytes as URL-safe text.
func random(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func newAttempt(next string) (attempt, error) {
	state, err := random(32)
	if err != nil {
		return attempt{}, err
	}
	nonce, err := random(32)
	if err != nil {
		return attempt{}, err
	}
	// RFC 7636 asks for 43 to 128 characters; 64 random bytes encode to 86.
	verifier, err := random(64)
	if err != nil {
		return attempt{}, err
	}
	return attempt{state: state, nonce: nonce, verifier: verifier, returnTo: safeReturnPath(next)}, nil
}

// challenge is the verifier's S256 challenge: what the provider is shown at
// the start, so that only the holder of the verifier can spend the code.
func (a attempt) challenge() string {
	digest := sha256.Sum256([]byte(a.verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// seal is the cookie's value: the attempt, signed with the session secret.
// It is signed and not encrypted. Everything in it is this browser's own
// sign-in, and the cookie is HttpOnly, so there is nobody to hide it from.
func (a attempt) seal(secret string, now time.Time) (string, error) {
	claims := jwt.MapClaims{
		"aud":   attemptAudience,
		"state": a.state,
		"nonce": a.nonce,
		"cv":    a.verifier,
		"rt":    a.returnTo,
		"iat":   now.Unix(),
		"exp":   now.Add(attemptLifetime).Unix(),
	}
	// The app's request rides in the same signed cookie, so nothing about
	// an app's sign-in is kept on the server until it has succeeded, and a
	// callback cannot be steered to another return address or challenge.
	if a.app != nil {
		claims["ar"] = a.app.redirect
		claims["ac"] = a.app.challenge
		claims["dv"] = a.app.device
		claims["ca"] = a.app.chooseAccount
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// reopenAttempt is the attempt this browser started, if the callback's
// state is its own.
func reopenAttempt(sealed, returnedState, secret string, now time.Time) (attempt, error) {
	if sealed == "" || returnedState == "" {
		return attempt{}, refused(codeInvalidState, "no sign-in in progress on this browser")
	}
	claims := jwt.MapClaims{}
	parsed, err := jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithAudience(attemptAudience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	).ParseWithClaims(sealed, claims, func(*jwt.Token) (any, error) { return []byte(secret), nil })
	if err != nil || !parsed.Valid {
		return attempt{}, refused(codeInvalidState, "the sign-in cookie expired or was not sealed here")
	}
	text := func(name string) string {
		value, _ := claims[name].(string)
		return value
	}
	found := attempt{state: text("state"), nonce: text("nonce"), verifier: text("cv"), returnTo: safeReturnPath(text("rt"))}
	if found.state == "" || found.nonce == "" || found.verifier == "" {
		return attempt{}, refused(codeInvalidState, "the sign-in cookie is incomplete")
	}
	if redirect := text("ar"); redirect != "" {
		chooseAccount, _ := claims["ca"].(bool)
		found.app = &appRequest{redirect: redirect, challenge: text("ac"), device: text("dv"), chooseAccount: chooseAccount}
	}
	if subtle.ConstantTimeCompare([]byte(found.state), []byte(returnedState)) != 1 {
		// The app's return address is known from this cookie, which this
		// tier sealed: a refusal can still go back to the app that asked.
		return attempt{app: found.app}, refused(codeInvalidState, "the state is not this browser's")
	}
	return found, nil
}

// scopes is what the provider is asked about the person: that they are
// someone (openid), their address, and their name.
const scopes = "openid email profile"

// authorizationURL is where the browser is sent: an authorization-code
// request with the PKCE challenge, the state and the nonce.
func authorizationURL(endpoint string, settings Settings, a attempt) string {
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {settings.ClientID},
		"redirect_uri":          {settings.RedirectURL},
		"scope":                 {scopes},
		"state":                 {a.state},
		"nonce":                 {a.nonce},
		"code_challenge":        {a.challenge()},
		"code_challenge_method": {"S256"},
	}
	// "Use a different account" in the app: the provider's account chooser.
	// Never prompt=login, which loops on Authentik 2026.8.
	if a.app != nil && a.app.chooseAccount {
		query.Set("prompt", "select_account")
	}
	separator := "?"
	if strings.Contains(endpoint, "?") {
		separator = "&"
	}
	return endpoint + separator + query.Encode()
}
