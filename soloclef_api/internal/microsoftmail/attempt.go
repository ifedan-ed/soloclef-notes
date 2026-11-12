package microsoftmail

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// cookieName is the attempt's cookie.
const cookieName = "notes_ms_mail_connect"

// attemptLifetime is how long a person has between pressing the button and
// finishing on Microsoft's pages, a second factor included.
const attemptLifetime = 10 * time.Minute

// attempt is what one connect carries from its start to Microsoft's
// callback. It lives in one sealed, HttpOnly cookie on the browser that
// pressed the button, sent only with the callback request, and nowhere on
// the server: any replica can finish the flow, but only for that browser.
// A callback opened anywhere else (a forged or replayed link, or someone
// luring another person through Microsoft's consent with their own link)
// has no matching cookie, so a mailbox is only ever linked to the account
// of the person who started the connect.
//
// The mailbox being reconnected is not in it: its id only chose the
// address Microsoft's page preselects, and which record a grant replaces
// is decided by which Microsoft account signed in (account_record.go).
type attempt struct {
	// State is what Microsoft echoes back; the callback's must equal it.
	State string `json:"state"`
	// Verifier is the PKCE code verifier. Microsoft is shown only its hash.
	Verifier string `json:"cv"`
	// UserID is the account connecting: the callback has no session, so
	// this is whose mail connection is written.
	UserID string `json:"uid"`
	// ReturnPath is where in the website to land, or "".
	ReturnPath string `json:"rp,omitempty"`
	// AppChallenge is set when the phone app started this connect through
	// its browser hand-off (internal/apphandoff): the S256 challenge of a
	// verifier only that app holds. Such an attempt saves nothing at the
	// callback. The browser that finishes it may be anybody's (whoever
	// opened the hand-off's link), so the mailbox is held, and the app
	// saves it by proving the verifier from its own session (SaveForApp).
	AppChallenge string `json:"app,omitempty"`
	// Expires is when the attempt runs out, in Unix seconds.
	Expires int64 `json:"exp"`
}

// errAttemptRefused: the callback is not the one this browser started, or
// it ran out.
var errAttemptRefused = errors.New("microsoftmail: no Microsoft connect in progress on this browser")

func randomText(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// safeReturnPath is a path of this site ("/dashboard/settings/connectors"),
// or "". Anything that a browser could read as another site is dropped: a
// scheme-relative "//host", a backslash, a control character.
func safeReturnPath(path string) string {
	if len(path) > 512 || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		strings.ContainsAny(path, "\\\r\n\t") {
		return ""
	}
	return path
}

func newAttempt(userID, returnPath string, now time.Time) (attempt, error) {
	state, err := randomText(32)
	if err != nil {
		return attempt{}, err
	}
	// RFC 7636: 43 to 128 characters; 64 random bytes encode to 86.
	verifier, err := randomText(64)
	if err != nil {
		return attempt{}, err
	}
	return attempt{State: state, Verifier: verifier, UserID: userID,
		ReturnPath: safeReturnPath(returnPath), Expires: now.Add(attemptLifetime).Unix()}, nil
}

// challenge is the S256 form of the verifier, which is all Microsoft's
// sign-in page is given.
func (a attempt) challenge() string {
	digest := sha256.Sum256([]byte(a.Verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// seal is the cookie's value: the attempt, encrypted and signed with the
// deployment's secret key.
func (a attempt) seal(secretKey string, now time.Time) (string, error) {
	plain, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	return storedtoken.Seal(secretKey, string(plain), now, rand.Reader)
}

// reopen is the attempt this browser started, if the callback's state is
// its own and it has not run out.
func reopen(sealed, returnedState, secretKey string, now time.Time) (attempt, error) {
	if sealed == "" || returnedState == "" {
		return attempt{}, errAttemptRefused
	}
	plain, err := storedtoken.Open(secretKey, sealed)
	if err != nil {
		return attempt{}, errAttemptRefused
	}
	var held attempt
	if json.Unmarshal([]byte(plain), &held) != nil || held.UserID == "" || held.Verifier == "" {
		return attempt{}, errAttemptRefused
	}
	if now.Unix() >= held.Expires {
		return attempt{}, errAttemptRefused
	}
	if subtle.ConstantTimeCompare([]byte(held.State), []byte(returnedState)) != 1 {
		return attempt{}, errAttemptRefused
	}
	held.ReturnPath = safeReturnPath(held.ReturnPath)
	return held, nil
}

// cookiePath is Microsoft's callback path: the only request the cookie
// goes with.
func cookiePath(redirectURI string) string {
	if parsed, err := url.Parse(redirectURI); err == nil && parsed.Path != "" {
		return parsed.Path
	}
	return "/"
}
