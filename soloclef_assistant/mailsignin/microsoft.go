package mailsignin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Exchange Online's IMAP endpoint, for Microsoft 365 and Outlook.com
// alike. Fixed here and never read from a stored mailbox, so an access
// token can only ever be sent to Microsoft.
const (
	IMAPHost = "outlook.office365.com"
	IMAPPort = 993
)

// TokenURL is Microsoft's token endpoint for a tenant ("common",
// "organizations", "consumers", a tenant id or a verified domain).
func TokenURL(tenant string) string {
	return "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/token"
}

// IMAPScope is the permission a mailbox is read with.
const IMAPScope = "https://outlook.office.com/IMAP.AccessAsUser.All"

// Scopes is what the sign-in asks for, and what every token request
// repeats. openid, email and profile make the id_token name the mailbox:
// Microsoft puts `email` in it only with the email scope, and
// `preferred_username` and `oid` only with profile. offline_access is the
// refresh token, so the mailbox stays connected after the first hour.
const Scopes = "openid email profile offline_access " + IMAPScope

// Grant is what Microsoft's token endpoint hands over.
type Grant struct {
	AccessToken string
	// RefreshToken is "" when Microsoft kept the previous one.
	RefreshToken string
	Expiry       time.Time
	// Scopes is what the person actually granted; Microsoft lets them
	// untick.
	Scopes []string
	// IDToken names the mailbox. Only a code exchange is read for one.
	IDToken string
}

// GrantsIMAP reports whether the person left mailbox access ticked.
// Microsoft returns the scope with or without its resource prefix and in
// either case, so only its last segment is compared.
func (g Grant) GrantsIMAP() bool {
	wanted := strings.ToLower(IMAPScope[strings.LastIndex(IMAPScope, "/")+1:])
	return slices.ContainsFunc(g.Scopes, func(scope string) bool {
		return strings.ToLower(scope[strings.LastIndex(scope, "/")+1:]) == wanted
	})
}

// The two failures are told apart because they need different fixes.
//
// GrantRevoked is a grant Microsoft no longer honours (consent removed,
// sign-in sessions revoked, unused for ninety days, a policy that now
// wants the person): only the mailbox's owner can fix it, by reconnecting.
// Anything else is EndpointError (Microsoft unreachable or erroring, or
// this deployment's client secret expired) and leaves the mailbox alone.
type GrantRevoked struct{ Code string }

func (e *GrantRevoked) Error() string {
	return "mailsignin: Microsoft refused the grant: " + e.Code
}

// EndpointError's Code is Microsoft's own error word, or "" when Microsoft
// was not reached. Its text is never a token or a reply's body.
type EndpointError struct{ Code, Reason string }

func (e *EndpointError) Error() string { return "mailsignin: " + e.Reason }

// needsThePerson are the OAuth errors that mean "this grant is dead; the
// person must sign in again".
var needsThePerson = []string{"invalid_grant", "interaction_required", "consent_required", "login_required"}

// tokenTimeout bounds one request to Microsoft's token endpoint.
const tokenTimeout = 10 * time.Second

// Endpoint is Microsoft's token endpoint as one app registration asks it.
type Endpoint struct {
	ClientID     string
	ClientSecret string
	// TokenURL is the tenant's token endpoint.
	TokenURL string
	http     *http.Client
}

// NewEndpoint is the endpoint over the process's transport.
func NewEndpoint(clientID, clientSecret, tokenURL string, transport http.RoundTripper) *Endpoint {
	return &Endpoint{ClientID: clientID, ClientSecret: clientSecret, TokenURL: tokenURL, http: &http.Client{
		Transport: transport,
		Timeout:   tokenTimeout,
		// A reply cannot send a code, a secret or a token anywhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Token posts a form to the token endpoint and reads the reply.
func (e *Endpoint) Token(ctx context.Context, form url.Values, now time.Time) (Grant, error) {
	form.Set("client_id", e.ClientID)
	// client_secret_post: what Microsoft's own samples for web apps use.
	form.Set("client_secret", e.ClientSecret)
	form.Set("scope", Scopes)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Grant{}, &EndpointError{Reason: "the token request could not be built"}
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := e.http.Do(request)
	if err != nil {
		// The error's text can repeat the request.
		return Grant{}, &EndpointError{Reason: "Microsoft's token endpoint could not be reached"}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Grant{}, &EndpointError{Reason: "Microsoft's reply could not be read"}
	}
	var reply struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		Scope        string `json:"scope"`
		// A number, though some gateways send it as text.
		ExpiresIn json.Number `json:"expires_in"`
		Error     string      `json:"error"`
	}
	_ = json.Unmarshal(raw, &reply)
	if response.StatusCode >= http.StatusBadRequest {
		if slices.Contains(needsThePerson, reply.Error) {
			return Grant{}, &GrantRevoked{Code: reply.Error}
		}
		// The error word says why and carries no token; it is bounded
		// because it is Microsoft's text, not this package's.
		code := reply.Error
		if len(code) > 64 {
			code = code[:64]
		}
		return Grant{}, &EndpointError{Code: code, Reason: "Microsoft answered " + response.Status + " to the token request (" + code + ")"}
	}
	if reply.AccessToken == "" {
		return Grant{}, &EndpointError{Reason: "Microsoft's token reply held no access token"}
	}
	lifetime, _ := reply.ExpiresIn.Int64()
	if lifetime < 0 {
		lifetime = 0
	}
	return Grant{AccessToken: reply.AccessToken, RefreshToken: reply.RefreshToken, IDToken: reply.IDToken,
		Scopes: strings.Fields(reply.Scope), Expiry: now.Add(time.Duration(lifetime) * time.Second)}, nil
}

// Refresh gets a new access token, and usually a new refresh token, for a
// stored grant.
func (e *Endpoint) Refresh(ctx context.Context, refreshToken string, now time.Time) (Grant, error) {
	return e.Token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}, now)
}

// Revoked reports whether err is Microsoft refusing the grant itself, and
// with which word.
func Revoked(err error) (string, bool) {
	var dead *GrantRevoked
	if errors.As(err, &dead) {
		return dead.Code, true
	}
	return "", false
}
