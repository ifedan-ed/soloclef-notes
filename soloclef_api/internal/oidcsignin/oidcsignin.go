// Package oidcsignin signs a person in to the web app through the
// deployment's OpenID Connect provider: single sign-on.
//
//	GET  /auth/oidc/authorize-redirect?next=   send the browser to the provider
//	GET  /auth/oidc/callback                   finish the sign-in it comes back with
//	GET  /api/v1/auth/oidc/app/start           the same, started by the phone app
//	POST /api/v1/auth/oidc/app/exchange        the app's session for its one-time code
//
// Any provider that speaks OpenID Connect will do (the deployment this was
// written for runs Authentik). It is an ordinary part of the product, on
// whenever OIDC_ENABLED says so: nothing about it is held behind a licence
// or an edition.
//
// # The flow
//
// An authorization-code flow with PKCE (S256), a state and a nonce:
//
//  1. authorize-redirect reads the provider's discovery document, makes the
//     three secrets of one sign-in (attempt.go), seals them in a short-lived
//     cookie on this browser and redirects to the provider.
//  2. The provider sends the browser back to the callback with a code and
//     the state. The cookie must be there and its state must be that one, so
//     a callback opened in any other browser has nothing to match.
//  3. The code is exchanged, with the PKCE verifier and the client secret,
//     for an ID token (exchange.go).
//  4. The ID token is verified against the provider's published keys, for
//     this client, from this issuer, inside its lifetime, and carrying this
//     sign-in's nonce. The verification is internal/agentsignin's: the same
//     provider signs an agent's token, and one verifier means the two
//     sign-ins cannot disagree about what a valid token is.
//  5. The identity is matched to an account by provider and subject in
//     `oauth_account`, or linked to one, or given a new one (link.go).
//  6. The session is made by internal/signin (StartWebSession): the same
//     cookies and the same refresh-token family a password sign-in gets.
//
// Every refusal ends on the web app's sign-in page as `/login?error=<code>`,
// where the page explains it; a bare JSON error in a browser tab explains
// nothing to the person looking at it.
//
// # What it does better than the Python
//
//   - **One switch says whether a first sign-in may make an account**
//     (SIGNUP_ALLOWED, the sign-up form's own switch), in place of a flag
//     and a list of domains with defaults. Who may reach the application at
//     all is the provider's decision, made before this tier sees a token.
//   - **A new account has no password**, where the Python stored the hash of
//     a random one nobody knew. The person sets one by the reset link if
//     they want one.
//   - **The link, the account and the sign-in stamp are one transaction**,
//     so a sign-in that fails half-way leaves no account without its link.
//   - **No second rate limit.** The Python counted these two routes on a
//     counter of their own; a callback reaches the provider only with this
//     browser's own cookie and state, so the tier's general limit is enough.
//
// # The phone app
//
// The phone app signs in through the same provider in its browser sheet
// (app_sign_in.go): /api/v1/auth/oidc/app/start starts it with the app's
// PKCE challenge, the callback above ends it with a one-time code sent back
// to the app's own address, and /api/v1/auth/oidc/app/exchange spends that
// code for the phone's session (internal/signin's StartPhoneSession, the
// same session a password sign-in on the phone gets).
package oidcsignin

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/agentsignin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signin"
)

// The paths, at the server's root beside the other sign-in routes. The
// callback's is the one registered at the provider.
const (
	AuthorizePath = "/auth/oidc/authorize-redirect"
	CallbackPath  = "/auth/oidc/callback"
)

// Public is every route: nobody is signed in yet on any of them. cmd/api
// mounts them outside the gate.
func Public() []string {
	return []string{
		http.MethodGet + " " + AuthorizePath,
		http.MethodGet + " " + CallbackPath,
		http.MethodGet + " " + AppStartPath,
		http.MethodPost + " " + AppExchangePath,
	}
}

// Served is every route of the package.
func Served() []string { return Public() }

// The codes a refused sign-in carries to the sign-in page, which words each
// one (soloclef_web/lib/auth-errors.ts; the two lists are kept in step).
const (
	codeInvalidState       = "OIDC_INVALID_STATE"
	codeUnavailable        = "OIDC_UNAVAILABLE"
	codeProviderRefused    = "OIDC_PROVIDER_ERROR"
	codeExchangeFailed     = "OIDC_EXCHANGE_FAILED"
	codeInvalidIDToken     = "OIDC_INVALID_ID_TOKEN"
	codeEmailNotVerified   = "OIDC_EMAIL_NOT_VERIFIED"
	codeAccountNotVerified = "OIDC_ACCOUNT_NOT_VERIFIED"
	codeNoAccount          = "OIDC_NO_ACCOUNT"
	codeInactive           = "USER_INACTIVE"
	// codeServerError is this tier's own fault (the database); the page
	// words "500" as a server error.
	codeServerError = "500"
)

// providerErrorsShownAsIs are the errors a provider may itself send back on
// the callback (RFC 6749 4.1.2.1) that the sign-in page already words. Any
// other becomes codeProviderRefused, so the page is never asked to show a
// string a provider, or whoever wrote the link, chose.
var providerErrorsShownAsIs = map[string]bool{
	"access_denied": true, "temporarily_unavailable": true, "server_error": true,
}

// refusal is a sign-in that ends on the sign-in page with its code
// explained. The reason is for the log, never for the browser.
type refusal struct {
	code   string
	reason string
}

func (r *refusal) Error() string { return r.code + ": " + r.reason }

func refused(code, reason string) error { return &refusal{code: code, reason: reason} }

// Settings is what the deployment says about its provider. Every field is
// required; cmd/api takes them from the configuration, which refuses to
// start with single sign-on on and one of them missing.
type Settings struct {
	// ClientID and ClientSecret are this application's at the provider.
	ClientID     string
	ClientSecret string
	// RedirectURL is the callback's public address, as registered there.
	RedirectURL string
	// FrontendURL is the web app's address: where a finished sign-in lands
	// and where a refused one is explained.
	FrontendURL string
	// ProviderName is `oauth_account.oauth_name` for every link this makes.
	// Changing it orphans them all. Agent sign-in finds an identity's
	// account under the same name (internal/agentsignin).
	ProviderName string
	// SignupAllowed says whether a first sign-in with an address no account
	// has makes one. It is the deployment's SIGNUP_ALLOWED, the switch the
	// sign-up form reads.
	SignupAllowed bool
	// AppRedirects is the phone app's return addresses (OIDC_APP_REDIRECTS):
	// the only places an app sign-in's code or refusal is ever sent. Empty
	// means the app may not sign in here this way.
	AppRedirects []string
}

// Routes is the two routes and what they share.
type Routes struct {
	// Enabled is the deployment's OIDC_ENABLED. With it false both routes
	// answer 404 and nothing below is read.
	Enabled  bool
	Settings Settings
	Pool     *pgxpool.Pool
	// Verifier judges the ID token and holds the provider's discovery
	// document and keys (its Directory).
	Verifier *agentsignin.Verifier
	// Sessions makes the session once the identity is settled.
	Sessions signin.Routes
	// Secret seals the attempt cookie: the session secret.
	Secret string
	// Codes holds the phone app's one-time codes (app_sign_in.go).
	Codes AppCodes
	// HTTPClient reaches the token endpoint; nil for one with a timeout
	// that follows no redirect.
	HTTPClient *http.Client
	Errors     errorenvelope.Writer
	Logger     *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

// NewVerifier is the verifier for a provider, by its issuer exactly as the
// provider prints it. The discovery document is read from under the issuer
// and must name that issuer back, so a mistyped address is a refused
// document and never somebody else's keys.
func NewVerifier(issuer, clientID, providerName string) *agentsignin.Verifier {
	settings := agentsignin.Settings{Issuer: issuer, ClientID: clientID, ProviderName: providerName}
	return &agentsignin.Verifier{
		Settings:  settings,
		Directory: &agentsignin.ProviderDirectory{DiscoveryURL: settings.DiscoveryURL()},
	}
}

// Register mounts both routes, whether or not single sign-on is on: the
// routes this tier answers are one fixed list (cmd/api), and a deployment
// with it off answers them 404.
func (r Routes) Register(api huma.API) {
	signin.Mount(api, http.MethodGet, AuthorizePath, "oidc-authorize", "Send the browser to single sign-on", r.authorize)
	signin.Mount(api, http.MethodGet, CallbackPath, "oidc-callback", "Finish a single sign-on", r.callback)
	signin.Mount(api, http.MethodGet, AppStartPath, "oidc-app-start", "Send the phone app's browser sheet to single sign-on", r.appStart)
	signin.Mount(api, http.MethodPost, AppExchangePath, "oidc-app-exchange", "The phone app's session for its one-time code", r.appExchange)
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

// off answers the 404 of a deployment without single sign-on, and reports
// whether it did.
func (r Routes) off(response http.ResponseWriter, request *http.Request) bool {
	if r.Enabled {
		return false
	}
	response.Header().Set("Cache-Control", "no-store")
	r.Errors.Write(response, http.StatusNotFound, "Single sign-on is not switched on for this server.", requestcontext.IDFrom(request))
	return true
}

// authorize starts a sign-in: `?next=/some/page` is where to land afterwards
// (a path on the web app; anything else lands on the dashboard).
func (r Routes) authorize(response http.ResponseWriter, request *http.Request) {
	if r.off(response, request) {
		return
	}
	provider, err := r.Verifier.Directory.Metadata(request.Context())
	if err != nil {
		r.toSignInPage(response, request, refused(codeUnavailable, err.Error()))
		return
	}
	started, err := newAttempt(request.URL.Query().Get("next"))
	if err != nil {
		r.toSignInPage(response, request, err)
		return
	}
	sealed, err := started.seal(r.Secret, r.now())
	if err != nil {
		r.toSignInPage(response, request, err)
		return
	}
	http.SetCookie(response, r.attemptCookie(sealed, attemptLifetime))
	redirect(response, authorizationURL(provider.AuthorizationEndpoint, r.Settings, started), http.StatusFound)
}

// callback finishes the sign-in the provider sent back, or says why it
// cannot.
func (r Routes) callback(response http.ResponseWriter, request *http.Request) {
	if r.off(response, request) {
		return
	}
	userID, started, err := r.signedIn(request)
	// A sign-in the phone app started ends in the app, refused or not.
	if started.app != nil {
		r.finishForApp(response, request, started.app, userID, err)
		return
	}
	if err != nil {
		r.toSignInPage(response, request, err)
		return
	}
	returnTo := started.returnTo
	// The cookies are set only now, when nothing is left that can refuse:
	// a refused sign-in must leave the browser exactly as signed out as it
	// was.
	if _, err := r.Sessions.StartWebSession(response, request, userID); err != nil {
		r.toSignInPage(response, request, err)
		return
	}
	http.SetCookie(response, r.attemptCookie("", -1))
	redirect(response, strings.TrimRight(r.Settings.FrontendURL, "/")+returnTo, http.StatusSeeOther)
}

// signedIn is steps 2 to 5 of the flow: the account the callback signs in
// as and the page to land on, or the refusal.
//
// The attempt is answered whenever its cookie could be read, refused or not,
// so that a refused sign-in the app started still goes back to the app.
func (r Routes) signedIn(request *http.Request) (userID string, started attempt, err error) {
	ctx := request.Context()
	query := request.URL.Query()
	sealed := ""
	if cookie, err := request.Cookie(attemptCookieName); err == nil {
		sealed = cookie.Value
	}
	started, err = reopenAttempt(sealed, query.Get("state"), r.Secret, r.now())
	// The provider's own refusal is shown as the provider's even when the
	// attempt cannot be found (a sign-in older than its cookie, cookies
	// blocked): "access denied" is the truer thing to tell the person.
	if code := query.Get("error"); code != "" {
		if !providerErrorsShownAsIs[code] {
			code = codeProviderRefused
		}
		return "", started, refused(code, "the provider answered "+query.Get("error"))
	}
	if err != nil {
		return "", started, err
	}
	code := query.Get("code")
	if code == "" {
		return "", started, refused(codeProviderRefused, "the callback carries no code")
	}

	provider, err := r.Verifier.Directory.Metadata(ctx)
	if err != nil {
		return "", started, refused(codeUnavailable, err.Error())
	}
	idToken, err := exchangeCode(ctx, r.httpClient(), provider.TokenEndpoint, r.Settings, started.verifier, code)
	if err != nil {
		return "", started, err
	}
	claims, err := r.Verifier.Claims(ctx, idToken)
	switch {
	case errors.Is(err, agentsignin.ErrProviderUnavailable):
		return "", started, refused(codeUnavailable, err.Error())
	case err != nil:
		return "", started, refused(codeInvalidIDToken, err.Error())
	}
	who := identityFrom(claims)
	// The nonce ties the token to this sign-in: a token the provider made
	// for another one, replayed here, carries another nonce.
	if subtle.ConstantTimeCompare([]byte(who.nonce), []byte(started.nonce)) != 1 {
		return "", started, refused(codeInvalidIDToken, "the ID token's nonce is not this sign-in's")
	}
	userID, how, err := accountFor(ctx, r.Pool, r.Settings, who, r.now())
	if err != nil {
		return "", started, err
	}
	r.Logger.InfoContext(ctx, "single sign-on", "user_id", userID, "match", string(how))
	return userID, started, nil
}

// toSignInPage ends a refused sign-in on the web app's sign-in page, and
// drops the attempt's cookie: a refused attempt is not to be finished later.
func (r Routes) toSignInPage(response http.ResponseWriter, request *http.Request, cause error) {
	code := r.refusalCode(request, cause)
	http.SetCookie(response, r.attemptCookie("", -1))
	redirect(response, strings.TrimRight(r.Settings.FrontendURL, "/")+"/login?"+url.Values{"error": {code}}.Encode(), http.StatusSeeOther)
}

// refusalCode is the code a refused sign-in is explained by, logged with
// its reason: the refusal's own, or "500" for this tier's fault.
func (r Routes) refusalCode(request *http.Request, cause error) string {
	var known *refusal
	if errors.As(cause, &known) {
		r.Logger.WarnContext(request.Context(), "single sign-on refused",
			"request_id", requestcontext.IDFrom(request), "code", known.code, "reason", known.reason)
		return known.code
	}
	if !errors.Is(cause, context.Canceled) {
		r.Logger.ErrorContext(request.Context(), "single sign-on could not be served",
			"request_id", requestcontext.IDFrom(request), "error", errText(cause))
	}
	return codeServerError
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// redirect sends the browser on. Never cached: each answer belongs to one
// sign-in.
func redirect(response http.ResponseWriter, location string, status int) {
	response.Header().Set("Location", location)
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
}

// attemptCookie is the cookie that ties a callback to the browser that
// started the sign-in. A lifetime below zero removes it.
//
// Lax, so it is sent on the provider's top-level redirect back; scoped to
// these two routes, so no other request carries it; and Secure whenever the
// callback's own address is https, which is known from the settings and
// does not depend on what a proxy header says.
func (r Routes) attemptCookie(value string, lifetime time.Duration) *http.Cookie {
	cookie := &http.Cookie{
		Name:     attemptCookieName,
		Value:    value,
		Path:     "/auth/oidc",
		MaxAge:   int(lifetime / time.Second),
		HttpOnly: true,
		Secure:   strings.HasPrefix(r.Settings.RedirectURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	}
	if lifetime < 0 {
		cookie.MaxAge = -1
	}
	return cookie
}

func (r Routes) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return defaultHTTPClient
}
