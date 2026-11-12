package signin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// The two password sign-ins. They check a password the same way and differ
// in what they hand back: the web app gets cookies, the phone app a token
// pair it keeps in the device's secure store.

// passwordSignIn is the web app's: a form with `username` (the address) and
// `password`, as the login page posts it.
func (r Routes) passwordSignIn(response http.ResponseWriter, request *http.Request) {
	body, ok := readBody(request)
	if !ok {
		r.refuse(response, request, http.StatusRequestEntityTooLarge, "The sign-in form is too large.")
		return
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		r.refuse(response, request, http.StatusBadRequest, badCredentials)
		return
	}
	userID, ok := r.checkPassword(response, request, form.Get("username"), form.Get("password"))
	if !ok {
		return
	}
	expires, err := r.StartWebSession(response, request, userID)
	if err != nil {
		r.fail(response, request, "could not start the sign-in", err)
		return
	}
	send(response, http.StatusOK, cookieSession{Authenticated: true, AccessExpiresAt: expires})
}

// StartWebSession signs a browser in as the account: it records a new
// sign-in (a refresh-token family), mints its access token, sets the two
// session cookies on the response, and answers when the access token
// expires. Nothing has been written to the response when it fails.
//
// It is the one place a browser's session is made. The password sign-in
// above calls it once the password is checked, and single sign-on
// (internal/oidcsignin) calls it once the provider's answer is, so the
// cookies, the lifetimes and the rotation that follows are the same however
// the person proved who they are. Whoever calls it has already decided that
// the account may sign in.
func (r Routes) StartWebSession(response http.ResponseWriter, request *http.Request, userID string) (accessExpiresAt int64, err error) {
	now := r.now()
	started, err := startFamily(request.Context(), r.Pool, userID, kindWeb, nil, now)
	if err != nil {
		return 0, err
	}
	access, expires, err := mintAccess(r.Secret, userID, started.familyID, now)
	if err != nil {
		return 0, fmt.Errorf("signing the access token: %w", err)
	}
	r.Cookies.write(response, request, access, started.token, now)
	return expires, nil
}

// phoneSignIn is the phone app's: JSON `{email, password, device_label}`.
// The device's name makes it a phone sign-in, which lives a year and is
// listed by that name; without one it is a browser-length sign-in.
func (r Routes) phoneSignIn(response http.ResponseWriter, request *http.Request) {
	body, ok := readBody(request)
	if !ok {
		r.refuse(response, request, http.StatusRequestEntityTooLarge, "The sign-in request is too large.")
		return
	}
	var asked struct {
		Email       string  `json:"email"`
		Password    string  `json:"password"`
		DeviceLabel *string `json:"device_label"`
	}
	if err := json.Unmarshal(body, &asked); err != nil {
		r.refuse(response, request, http.StatusBadRequest, badCredentials)
		return
	}
	userID, ok := r.checkPassword(response, request, asked.Email, asked.Password)
	if !ok {
		return
	}
	pair, err := r.startTokenSession(request.Context(), userID, asked.DeviceLabel)
	if err != nil {
		r.fail(response, request, "could not start the sign-in", err)
		return
	}
	send(response, http.StatusOK, pair)
}

// StartPhoneSession signs the phone app in as the account: a new sign-in
// (a refresh-token family) of the phone's kind under the device's name, its
// first access token, and the pair the app keeps in its secure store.
//
// It is the phone's StartWebSession. The password sign-in above uses it once
// the password is checked, and the app's single sign-on
// (internal/oidcsignin, the exchange of its one-time code) once the
// provider's answer is, so the lifetime, the devices list's row and the
// rotation that follows are the same however the person proved who they
// are. A phone that sends no name gets a browser-length sign-in, as the
// password route always gave it; the app always sends one.
func (r Routes) StartPhoneSession(ctx context.Context, userID string, deviceLabel *string) (TokenPair, error) {
	return r.startTokenSession(ctx, userID, deviceLabel)
}

func (r Routes) startTokenSession(ctx context.Context, userID string, deviceLabel *string) (TokenPair, error) {
	kind, device := kindWeb, deviceName(deviceLabel)
	if device != nil {
		kind = kindPhone
	}
	now := r.now()
	started, err := startFamily(ctx, r.Pool, userID, kind, device, now)
	if err != nil {
		return TokenPair{}, err
	}
	access, expires, err := mintAccess(r.Secret, userID, started.familyID, now)
	if err != nil {
		return TokenPair{}, fmt.Errorf("signing the access token: %w", err)
	}
	refresh := started.token
	return TokenPair{
		AccessToken: access, RefreshToken: &refresh, TokenType: "bearer",
		AccessExpiresAt: expires, SessionID: &started.familyID,
	}, nil
}

// deviceName is the phone's name with its whitespace collapsed, at most the
// 100 characters the column holds; nothing, when it names nothing.
func deviceName(label *string) *string {
	if label == nil {
		return nil
	}
	name := strings.Join(strings.Fields(*label), " ")
	if runes := []rune(name); len(runes) > 100 {
		name = string(runes[:100])
	}
	if name == "" {
		return nil
	}
	return &name
}

// checkPassword answers the account the address and password sign in as,
// or writes the refusal and answers false.
//
// Every way of being refused is the same answer and costs the same time: an
// address with no account, or an account with no password, verifies the
// password against a fixed hash anyway.
func (r Routes) checkPassword(response http.ResponseWriter, request *http.Request, address, password string) (string, bool) {
	ctx := request.Context()
	address = strings.TrimSpace(address)
	// A password longer than any new one may be is refused unhashed: it
	// cannot match, and hashing it would be work done for an attacker.
	if address == "" || password == "" || utf8.RuneCountInString(password) > maxPasswordLength {
		r.refuse(response, request, http.StatusBadRequest, badCredentials)
		return "", false
	}
	account, err := New(r.Pool).AccountForSignIn(ctx, address)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && account.HashedPassword == nil) {
		r.Hasher.SpendTime(ctx, password)
		r.refuse(response, request, http.StatusBadRequest, badCredentials)
		return "", false
	}
	if err != nil {
		r.fail(response, request, "could not look the account up", err)
		return "", false
	}
	matches, stale, err := r.Hasher.Verify(ctx, password, *account.HashedPassword)
	if err != nil {
		r.fail(response, request, "could not verify the password", err)
		return "", false
	}
	if !matches || !account.IsActive {
		r.refuse(response, request, http.StatusBadRequest, badCredentials)
		return "", false
	}
	r.recordSignIn(ctx, account.ID, password, stale)
	return account.ID, true
}

// recordSignIn stamps the sign-in and, when the stored hash was made with
// weaker settings, replaces it. Neither may cost the person their sign-in,
// so a failure is logged and the sign-in goes on.
func (r Routes) recordSignIn(ctx context.Context, userID, password string, stale bool) {
	now := r.now()
	var rehashed *string
	if stale {
		if hash, err := r.Hasher.Hash(ctx, password); err == nil {
			rehashed = &hash
		}
	}
	if err := New(r.Pool).RecordSignIn(ctx, RecordSignInParams{SignedInAt: &now, Rehashed: rehashed, UserID: userID}); err != nil {
		r.Logger.WarnContext(ctx, "could not record a sign-in", "error", err.Error())
	}
}

// cookieSession is what a cookie sign-in or refresh answers: the tokens are
// in the cookies, and the page is told only when to refresh.
type cookieSession struct {
	Authenticated   bool  `json:"authenticated"`
	AccessExpiresAt int64 `json:"access_expires_at"`
}

// TokenPair is what the phone app keeps. RefreshToken is null in the grace
// case of a refresh, and the app then keeps the one it has. SessionID is the
// sign-in's family, by which the app finds its own row in the devices list
// and binds its push registration.
type TokenPair struct {
	AccessToken     string  `json:"access_token"`
	RefreshToken    *string `json:"refresh_token"`
	TokenType       string  `json:"token_type"`
	AccessExpiresAt int64   `json:"access_expires_at"`
	SessionID       *string `json:"session_id"`
}
