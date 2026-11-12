package oidcsignin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/valkey-io/valkey-go"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// The phone app's single sign-on.
//
// The app cannot read the cookies a web sign-in ends with, so it signs in
// through the same provider in the phone's browser sheet and asks for a
// different ending:
//
//	GET  /api/v1/auth/oidc/app/start?app_redirect=soloclef://auth
//	     &challenge=<S256>&challenge_method=S256&device=<label>[&choose_account=1]
//	  -> 302 to the provider, the attempt (with the app's request) sealed in
//	     the same cookie a web sign-in uses
//	GET  /auth/oidc/callback  (the provider sends the sheet back here, as
//	     for the web)
//	  -> 303 to soloclef://auth?code=<one-time code>, or ?error=<code>
//	POST /api/v1/auth/oidc/app/exchange {code, verifier, device_label}
//	  -> the phone's session: {access_token, refresh_token, token_type,
//	     access_expires_at, session_id, end_session_url}
//
// The one-time code is useless to anyone but the app that started the
// sign-in: it is bound to the app's PKCE challenge (RFC 7636, S256), so
// another app on the phone that catches the soloclef:// redirect holds a code
// it cannot spend. It lives a minute, is spent once whoever presents it, and
// is stored only as a hash.
//
// A refusal the app should explain goes back to the app (the sheet closes
// and the app words the code, features/auth/reasons.ts); only a start whose
// return address is not on the list is answered here, because there is
// nowhere safe to send it.
//
// What it does better than the Python: the codes live only in Valkey, shared
// by every replica, and a Valkey that is down fails the sign-in with a
// reason, where the Python fell back to one process's memory and then
// refused the exchange whenever another replica served it.

// The app's two paths, beside the website's.
const (
	AppStartPath    = "/api/v1/auth/oidc/app/start"
	AppExchangePath = "/api/v1/auth/oidc/app/exchange"
)

// The codes a refused app sign-in carries, which the app words.
const (
	codeAppRedirect = "APP_REDIRECT_NOT_ALLOWED"
	codeAppBadPKCE  = "APP_BAD_CHALLENGE"
	codeAppExpired  = "APP_CODE_EXPIRED"
	codeAppVerifier = "APP_WRONG_VERIFIER"
)

// appCodeLifetime is how long the app has to spend its code once the sheet
// comes back: a minute. The app spends it at once; past this it says
// "Sign-in took too long. Try again."
const appCodeLifetime = time.Minute

// deviceLabelMax is the devices list's column (refresh_tokens.device_label).
const deviceLabelMax = 100

// A challenge is base64url(sha256(verifier)), exactly 43 characters; a
// verifier is 43 to 128 characters of RFC 7636's unreserved set.
var (
	challengeShape = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	verifierShape  = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

// appRequest is what an app sign-in carries from its start to its callback,
// sealed in the attempt cookie with the rest of the attempt.
type appRequest struct {
	redirect      string
	challenge     string
	device        string
	chooseAccount bool
}

// AppGrant is what a one-time code stands for: whose sign-in, for which
// app's challenge, from which device.
type AppGrant struct {
	UserID    string `json:"user_id"`
	Challenge string `json:"challenge"`
	Device    string `json:"device"`
}

// AppCodes holds the one-time codes between the callback and the exchange.
type AppCodes interface {
	// Issue stores the grant under a new code and answers the code.
	Issue(ctx context.Context, grant AppGrant) (string, error)
	// Redeem answers the grant behind the code and forgets it, so a second
	// redeem finds nothing; false when the code is unknown or expired.
	Redeem(ctx context.Context, code string) (AppGrant, bool, error)
}

// newAppCode is 32 random bytes as URL-safe text.
func newAppCode() (string, error) { return random(32) }

// appCodeKey is where a code's grant is kept: under the code's hash, so a
// dump of the store names no code anyone could spend.
func appCodeKey(code string) string {
	digest := sha256.Sum256([]byte(code))
	return "soloclef:app_sign_in:" + hex.EncodeToString(digest[:])
}

// ValkeyAppCodes keeps the codes in the deployment's Valkey, so the replica
// that serves the exchange need not be the one that served the callback. It
// connects on first use, like the tier's other Valkey users.
type ValkeyAppCodes struct {
	option valkey.ClientOption

	mu     sync.Mutex
	client valkey.Client
}

// NewValkeyAppCodes reads the address. It does not connect.
func NewValkeyAppCodes(address string) (*ValkeyAppCodes, error) {
	option, err := valkey.ParseURL(address)
	if err != nil {
		return nil, fmt.Errorf("oidcsignin: valkey address: %w", err)
	}
	// A code is read once, to be spent: no client-side copy may outlive it.
	option.DisableCache = true
	return &ValkeyAppCodes{option: option}, nil
}

func (v *ValkeyAppCodes) connection() (valkey.Client, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		return v.client, nil
	}
	client, err := valkey.NewClient(v.option)
	if err != nil {
		return nil, fmt.Errorf("oidcsignin: valkey: %w", err)
	}
	v.client = client
	return client, nil
}

// Close releases the connections.
func (v *ValkeyAppCodes) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		v.client.Close()
		v.client = nil
	}
}

func (v *ValkeyAppCodes) Issue(ctx context.Context, grant AppGrant) (string, error) {
	client, err := v.connection()
	if err != nil {
		return "", err
	}
	code, err := newAppCode()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(grant)
	if err != nil {
		return "", err
	}
	// NX: a code is 256 random bits, so a collision is not a thing that
	// happens; if it ever did, the existing grant is not overwritten.
	stored, err := client.Do(ctx, client.B().Set().Key(appCodeKey(code)).Value(string(payload)).Nx().
		Ex(appCodeLifetime).Build()).AsBool()
	if err != nil && !valkey.IsValkeyNil(err) {
		return "", err
	}
	if !stored {
		return "", errors.New("oidcsignin: a new one-time code was already taken")
	}
	return code, nil
}

func (v *ValkeyAppCodes) Redeem(ctx context.Context, code string) (AppGrant, bool, error) {
	client, err := v.connection()
	if err != nil {
		return AppGrant{}, false, err
	}
	// GETDEL: read and forget in one step, so two exchanges racing with one
	// code cannot both find it.
	payload, err := client.Do(ctx, client.B().Getdel().Key(appCodeKey(code)).Build()).ToString()
	if valkey.IsValkeyNil(err) {
		return AppGrant{}, false, nil
	}
	if err != nil {
		return AppGrant{}, false, err
	}
	var grant AppGrant
	if json.Unmarshal([]byte(payload), &grant) != nil || grant.UserID == "" || grant.Challenge == "" {
		return AppGrant{}, false, nil
	}
	return grant, true, nil
}

// readAppRequest is the app's request on /app/start, or the refusal's code.
// The return address is checked first and on its own: only an address on
// the list is ever redirected to, so a forged start cannot send a code, or
// even an error, anywhere else.
func (r Routes) readAppRequest(query url.Values) (appRequest, string) {
	redirect := strings.TrimSpace(query.Get("app_redirect"))
	if redirect == "" || !slices.Contains(r.Settings.AppRedirects, redirect) {
		return appRequest{}, codeAppRedirect
	}
	app := appRequest{redirect: redirect}
	method := strings.TrimSpace(query.Get("challenge_method"))
	app.challenge = strings.TrimSpace(query.Get("challenge"))
	if (method != "" && method != "S256") || !challengeShape.MatchString(app.challenge) {
		return app, codeAppBadPKCE
	}
	app.device = deviceLabel(query.Get("device"))
	switch strings.ToLower(strings.TrimSpace(query.Get("choose_account"))) {
	case "1", "true", "yes":
		app.chooseAccount = true
	}
	return app, ""
}

// deviceLabel is a device's name with its whitespace collapsed, at most the
// column's width.
func deviceLabel(value string) string {
	label := strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(label) > deviceLabelMax {
		label = string([]rune(label)[:deviceLabelMax])
	}
	return label
}

// appReturn is where the sheet is sent back: the app's address with a code,
// or with an error.
func appReturn(redirect, name, value string) string {
	separator := "?"
	if strings.Contains(redirect, "?") {
		separator = "&"
	}
	return redirect + separator + url.Values{name: {value}}.Encode()
}

// appStart starts an app's sign-in. It is the website's authorize with the
// app's request sealed into the attempt, so the callback knows to end it
// with a code for the app.
func (r Routes) appStart(response http.ResponseWriter, request *http.Request) {
	if r.off(response, request) {
		return
	}
	app, refusedCode := r.readAppRequest(request.URL.Query())
	if refusedCode == codeAppRedirect {
		response.Header().Set("Cache-Control", "no-store")
		r.Errors.WriteCoded(response, http.StatusBadRequest, codeAppRedirect, codeAppRedirect, requestcontext.IDFrom(request))
		return
	}
	if refusedCode != "" {
		r.toApp(response, request, app.redirect, refused(refusedCode, "the app's challenge is not an S256 challenge"))
		return
	}
	provider, err := r.Verifier.Directory.Metadata(request.Context())
	if err != nil {
		r.toApp(response, request, app.redirect, refused(codeUnavailable, err.Error()))
		return
	}
	started, err := newAttempt("")
	if err != nil {
		r.toApp(response, request, app.redirect, err)
		return
	}
	started.app = &app
	sealed, err := started.seal(r.Secret, r.now())
	if err != nil {
		r.toApp(response, request, app.redirect, err)
		return
	}
	http.SetCookie(response, r.attemptCookie(sealed, attemptLifetime))
	redirect(response, authorizationURL(provider.AuthorizationEndpoint, r.Settings, started), http.StatusFound)
}

// finishForApp ends an app's sign-in at the callback: a one-time code for
// the app, or the refusal, sent back to the app's own address. Nothing is
// set in the sheet's browser: the session is the app's, made at the
// exchange.
func (r Routes) finishForApp(response http.ResponseWriter, request *http.Request, app *appRequest, userID string, cause error) {
	if cause != nil {
		r.toApp(response, request, app.redirect, cause)
		return
	}
	code, err := r.Codes.Issue(request.Context(), AppGrant{UserID: userID, Challenge: app.challenge, Device: app.device})
	if err != nil {
		r.toApp(response, request, app.redirect, fmt.Errorf("keeping the app's one-time code: %w", err))
		return
	}
	http.SetCookie(response, r.attemptCookie("", -1))
	redirect(response, appReturn(app.redirect, "code", code), http.StatusSeeOther)
}

// toApp ends a refused app sign-in back in the app, with the refusal's
// code, and drops the attempt as toSignInPage does.
func (r Routes) toApp(response http.ResponseWriter, request *http.Request, appRedirect string, cause error) {
	code := r.refusalCode(request, cause)
	http.SetCookie(response, r.attemptCookie("", -1))
	redirect(response, appReturn(appRedirect, "error", code), http.StatusSeeOther)
}

// appSession is the exchange's answer: the phone's token pair and the
// provider's sign-out page.
type appSession struct {
	AccessToken     string  `json:"access_token"`
	RefreshToken    *string `json:"refresh_token"`
	TokenType       string  `json:"token_type"`
	AccessExpiresAt int64   `json:"access_expires_at"`
	SessionID       *string `json:"session_id"`
	// EndSessionURL is the provider's own sign-out page, which the app
	// opens at sign-out so the provider forgets the person too. Only an
	// https address is given: anything else is not a page to open.
	EndSessionURL *string `json:"end_session_url"`
}

// exchangeBodyMax bounds the exchange's body: a code, a verifier, a name.
const exchangeBodyMax = 4 << 10

// appExchange spends the app's one-time code, proved by the verifier behind
// the challenge it started with, for the phone's session.
func (r Routes) appExchange(response http.ResponseWriter, request *http.Request) {
	if r.off(response, request) {
		return
	}
	requestID := requestcontext.IDFrom(request)
	refuse := func(status int, code string) {
		response.Header().Set("Cache-Control", "no-store")
		// The app reads the code from the message (features/auth/sso.ts
		// compares it with the codes it words), and the code field says it
		// too.
		r.Errors.WriteCoded(response, status, code, code, requestID)
	}
	var asked struct {
		Code        string `json:"code"`
		Verifier    string `json:"verifier"`
		DeviceLabel string `json:"device_label"`
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, exchangeBodyMax+1))
	if err != nil || len(body) > exchangeBodyMax || json.Unmarshal(body, &asked) != nil {
		refuse(http.StatusBadRequest, codeAppExpired)
		return
	}
	if asked.Code == "" || len(asked.Code) > 256 {
		refuse(http.StatusBadRequest, codeAppExpired)
		return
	}
	// Spent on the way out: a code is redeemed once, whoever presents it. A
	// wrong verifier therefore spends it too, and the real app, whose code
	// it was, signs in again.
	grant, found, err := r.Codes.Redeem(request.Context(), asked.Code)
	if err != nil {
		r.Logger.ErrorContext(request.Context(), "the app's one-time code could not be read", "request_id", requestID, "error", err.Error())
		refuse(http.StatusServiceUnavailable, codeUnavailable)
		return
	}
	if !found {
		refuse(http.StatusBadRequest, codeAppExpired)
		return
	}
	if !verifierMatches(asked.Verifier, grant.Challenge) {
		r.Logger.WarnContext(request.Context(), "an app sign-in exchange with a verifier that is not the challenge's", "request_id", requestID)
		refuse(http.StatusBadRequest, codeAppVerifier)
		return
	}
	active, err := New(r.Pool).AccountIsActive(request.Context(), grant.UserID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		r.Logger.ErrorContext(request.Context(), "the account of an app sign-in could not be read", "request_id", requestID, "error", err.Error())
		refuse(http.StatusInternalServerError, codeServerError)
		return
	}
	if !active {
		refuse(http.StatusUnauthorized, codeInactive)
		return
	}
	device := deviceLabel(asked.DeviceLabel)
	if device == "" {
		device = grant.Device
	}
	pair, err := r.Sessions.StartPhoneSession(request.Context(), grant.UserID, &device)
	if err != nil {
		r.Logger.ErrorContext(request.Context(), "an app sign-in's session could not be made", "request_id", requestID, "error", err.Error())
		refuse(http.StatusInternalServerError, codeServerError)
		return
	}
	answer := appSession{
		AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, TokenType: pair.TokenType,
		AccessExpiresAt: pair.AccessExpiresAt, SessionID: pair.SessionID,
	}
	// The sign-out page is a courtesy: a provider that cannot be read now
	// does not stop the sign-in.
	if provider, err := r.Verifier.Directory.Metadata(request.Context()); err == nil && strings.HasPrefix(provider.EndSessionEndpoint, "https://") {
		answer.EndSessionURL = &provider.EndSessionEndpoint
	}
	r.Logger.InfoContext(request.Context(), "app single sign-on", "user_id", grant.UserID, "device", device)
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(answer)
}

// verifierMatches reports whether verifier is the one behind challenge:
// RFC 7636 S256, compared in constant time.
func verifierMatches(verifier, challenge string) bool {
	if !verifierShape.MatchString(verifier) {
		return false
	}
	digest := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(digest[:])), []byte(challenge)) == 1
}
