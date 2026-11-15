package jmapconnect

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// cookieName is the attempt's cookie.
const cookieName = "notes_jmap_mail_connect"

// attemptLifetime is how long a person has between pressing the button
// and finishing on the server's pages, a second factor included.
const attemptLifetime = 10 * time.Minute

// attempt is what one sign-in carries from its start to the callback, in
// one sealed HttpOnly cookie on the browser that started it and nowhere on
// the server. The server's endpoints are in it, read at the start from the
// server the person named, so the callback trades the code at the token
// endpoint that was checked and never at one a request names.
type attempt struct {
	State    string `json:"state"`
	Verifier string `json:"cv"`
	UserID   string `json:"uid"`
	// ServerURL is the server's own origin (Metadata.Server, which may
	// not be the address typed); Issuer, TokenEndpoint and
	// RevocationEndpoint are its metadata's.
	ServerURL          string `json:"srv"`
	Issuer             string `json:"iss"`
	IssuerInResponse   bool   `json:"issr,omitempty"`
	TokenEndpoint      string `json:"tok"`
	RevocationEndpoint string `json:"rev,omitempty"`
	ReturnPath         string `json:"rp,omitempty"`
	Expires            int64  `json:"exp"`
}

// errAttemptRefused: the callback is not the one this browser started, or
// it ran out.
var errAttemptRefused = errors.New("jmapconnect: no sign-in in progress on this browser")

func randomText(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// safeReturnPath is a path of this site, or "": anything a browser could
// read as another site is dropped.
func safeReturnPath(path string) string {
	if len(path) > 512 || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		strings.ContainsAny(path, "\\\r\n\t") {
		return ""
	}
	return path
}

func newAttempt(userID, serverURL string, metadata jmapaccounts.Metadata, returnPath string, now time.Time) (attempt, error) {
	state, err := randomText(32)
	if err != nil {
		return attempt{}, err
	}
	// RFC 7636: 43 to 128 characters; 64 random bytes encode to 86.
	verifier, err := randomText(64)
	if err != nil {
		return attempt{}, err
	}
	return attempt{State: state, Verifier: verifier, UserID: userID, ServerURL: serverURL,
		Issuer: metadata.Issuer, IssuerInResponse: metadata.IssuerInResponse,
		TokenEndpoint: metadata.TokenEndpoint, RevocationEndpoint: metadata.RevocationEndpoint,
		ReturnPath: safeReturnPath(returnPath), Expires: now.Add(attemptLifetime).Unix()}, nil
}

// challenge is the S256 form of the verifier: all the server is shown.
func (a attempt) challenge() string {
	digest := sha256.Sum256([]byte(a.Verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// metadata is the server's endpoints as the attempt holds them.
func (a attempt) metadata() jmapaccounts.Metadata {
	return jmapaccounts.Metadata{Issuer: a.Issuer, TokenEndpoint: a.TokenEndpoint, RevocationEndpoint: a.RevocationEndpoint,
		IssuerInResponse: a.IssuerInResponse}
}

// seal is the cookie's value: the attempt encrypted and signed with the
// deployment's secret key, so the verifier is not readable from it.
func (a attempt) seal(secretKey string, now time.Time) (string, error) {
	plain, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	return storedtoken.Seal(secretKey, string(plain), now, rand.Reader)
}

// reopen is the attempt this browser started, if the callback's state is
// its own, its issuer the server's, and it has not run out.
func reopen(sealed, returnedState, returnedIssuer, secretKey string, now time.Time) (attempt, error) {
	if sealed == "" || returnedState == "" {
		return attempt{}, errAttemptRefused
	}
	plain, err := storedtoken.Open(secretKey, sealed)
	if err != nil {
		return attempt{}, errAttemptRefused
	}
	var held attempt
	if json.Unmarshal([]byte(plain), &held) != nil || held.UserID == "" || held.Verifier == "" || held.TokenEndpoint == "" {
		return attempt{}, errAttemptRefused
	}
	if now.Unix() >= held.Expires || subtle.ConstantTimeCompare([]byte(held.State), []byte(returnedState)) != 1 {
		return attempt{}, errAttemptRefused
	}
	// RFC 9207: a server that says it adds `iss` must have added its own,
	// which is what stops a code from another server being played here.
	if held.IssuerInResponse && returnedIssuer != held.Issuer {
		return attempt{}, errAttemptRefused
	}
	held.ReturnPath = safeReturnPath(held.ReturnPath)
	return held, nil
}
