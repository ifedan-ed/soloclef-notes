package searchconnectors

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// An MCP server's OAuth sign-in, from the press of "Sign in" to the
// authorization server's callback.
//
// The attempt lives in one sealed, HttpOnly cookie on the browser that
// started it, scoped to the callback's path, and nowhere on the server: any
// replica can finish it, but only for that browser. The cookie is sealed
// (sealedsecret, the OAuth attempt purpose) with the `state` sent to the
// authorization server bound in as associated data, so the cookie opens only
// for a callback that carries its own state: a forged or replayed callback,
// or one meant for another browser, opens nothing. The account is inside
// the sealed text, so the sign-in is written to the account that started it
// and no other, whoever's browser finishes it.

// oauthCookieName is the attempt's cookie.
const oauthCookieName = "notes_mcp_oauth"

// oauthAttemptLifetime is how long a person has between pressing "Sign in"
// and finishing on the provider's pages, a second factor included.
const oauthAttemptLifetime = 10 * time.Minute

// oauthAttempt is what one sign-in carries from its start to the callback.
type oauthAttempt struct {
	Verifier    string `json:"cv"`
	UserID      string `json:"uid"`
	ConnectorID int64  `json:"cid"`
	// ServerURL is the server's stored (masked) address when the sign-in
	// started: the tokens are written only to a row still pointing there.
	ServerURL string `json:"url"`
	// What discovery found, so the callback does not discover again.
	Issuer             string `json:"iss"`
	TokenEndpoint      string `json:"token"`
	RevocationEndpoint string `json:"revoke,omitempty"`
	Resource           string `json:"res"`
	Scope              string `json:"scope,omitempty"`
	// The client. A dynamically registered client's secret travels here,
	// sealed, until the callback stores it; a typed client's stays in the
	// row and is not repeated.
	ClientID     string `json:"client"`
	ClientSecret string `json:"secret,omitempty"`
	ClientAuth   string `json:"auth"`
	Dynamic      bool   `json:"dyn"`
	ReturnPath   string `json:"rp,omitempty"`
	Expires      int64  `json:"exp"`
}

// errAttemptRefused: no sign-in in progress on this browser for this state,
// or one that ran out.
var errAttemptRefused = errors.New("searchconnectors: no MCP sign-in in progress on this browser")

// newState is a fresh `state`: 32 random bytes.
func newState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// sealAttempt is the cookie's value.
func sealAttempt(seal sealedsecret.Seal, state string, attempt oauthAttempt) (string, error) {
	plain, err := json.Marshal(attempt)
	if err != nil {
		return "", err
	}
	return seal.Seal(state, string(plain))
}

// openAttempt is the attempt this browser started for this state, if it has
// not run out.
func openAttempt(seal sealedsecret.Seal, sealed, state string, now time.Time) (oauthAttempt, error) {
	if sealed == "" || state == "" {
		return oauthAttempt{}, errAttemptRefused
	}
	plain, err := seal.Open(state, sealed)
	if err != nil {
		return oauthAttempt{}, errAttemptRefused
	}
	var held oauthAttempt
	if json.Unmarshal([]byte(plain), &held) != nil || held.UserID == "" || held.Verifier == "" || held.ConnectorID == 0 {
		return oauthAttempt{}, errAttemptRefused
	}
	if now.Unix() >= held.Expires {
		return oauthAttempt{}, errAttemptRefused
	}
	held.ReturnPath = safeReturnPath(held.ReturnPath)
	return held, nil
}

// safeReturnPath is a path of this site ("/dashboard/settings/connectors"),
// or "". Anything a browser could read as another site is dropped: a
// scheme-relative "//host", a backslash, a control character.
func safeReturnPath(path string) string {
	if len(path) > 512 || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		strings.ContainsAny(path, "\\\r\n\t") {
		return ""
	}
	return path
}

// cookiePathOf is the callback's path, the only request the cookie goes
// with.
func cookiePathOf(redirectURI string) string {
	if parsed, err := url.Parse(redirectURI); err == nil && parsed.Path != "" {
		return parsed.Path
	}
	return "/"
}
