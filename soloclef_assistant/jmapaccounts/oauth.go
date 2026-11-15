package jmapaccounts

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

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

// MailScope is the one scope a mailbox's grant asks for: JMAP mail and
// submission, nothing of the account's contacts, calendars, files,
// security or settings (Stalwart's scopes page).
const MailScope = "urn:ietf:params:oauth:scope:mail"

// OAuth is the requests made to a Stalwart server's OAuth endpoints: its
// metadata, the code exchange, a refresh and a revocation. This app is a
// public client identified by a client ID metadata document (Stalwart's
// "metadata document" client kind): its client id is the https address of
// a JSON document this deployment serves, which names its redirect URI.
// Stalwart's open dynamic registration (/auth/register) does not serve a
// web app: it refuses https redirect URIs and accepts only loopback and
// private-use schemes.
type OAuth struct {
	// Network reaches the server's own host only, as every JMAP request
	// does (jmapmail.Network).
	Network jmapmail.Network
	// Timeout bounds one request.
	Timeout time.Duration
}

// Check reports what is missing.
func (o OAuth) Check() error {
	if o.Network == nil || o.Timeout <= 0 {
		return errors.New("jmapaccounts: OAuth needs a Network and a positive Timeout")
	}
	return nil
}

// Metadata is what a server's RFC 8414 document says this app needs.
type Metadata struct {
	// Server is the origin the whole sign-in is made with: the attempt,
	// the token exchange, the JMAP session and the stored mailbox. It is
	// the issuer's origin, which is the address typed or the one address
	// that address's metadata named and that confirmed it (ownServer).
	Server                string
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RevocationEndpoint    string
	// IssuerInResponse says the server adds `iss` to its redirect (RFC
	// 9207); the callback then requires it and checks it.
	IssuerInResponse bool
}

// mostOAuthBytes bounds any reply of an OAuth endpoint: a token response
// is well under a kilobyte, metadata a few.
const mostOAuthBytes = 64 << 10

// What a person is told.
const (
	noOAuth       = "This server does not offer Stalwart's sign-in for mail apps. Connect with an app password instead."
	oauthRejected = "The server refused this sign-in. Connect the mailbox again."
)

// Refused is a server refusing what was asked, in words for the person.
type Refused struct{ Message string }

func (r *Refused) Error() string { return r.Message }

// ErrGrantRefused is the server refusing the grant itself (invalid_grant,
// invalid_client): the mailbox must be connected again.
var ErrGrantRefused = &Refused{Message: oauthRejected}

// request makes one request to an OAuth endpoint on origin and reads the
// reply, bounded.
func (o OAuth) request(ctx context.Context, origin string, request *http.Request) (int, []byte, error) {
	client, err := o.Network.Client(origin)
	if err != nil {
		return 0, nil, &Refused{Message: "Could not connect to this mail server"}
	}
	if got, err := jmapmail.Origin(request.URL.String()); err != nil || got != origin {
		return 0, nil, &Refused{Message: "The server's sign-in is on another server, which is not followed"}
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, &Refused{Message: "Could not connect to this mail server"}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, mostOAuthBytes))
	if err != nil {
		return 0, nil, &Refused{Message: "The mail server's answer could not be read"}
	}
	return response.StatusCode, body, nil
}

// metadataPath is where RFC 8414 3 puts a server's metadata.
const metadataPath = "/.well-known/oauth-authorization-server"

// serverDocument is the part of a server's metadata read here.
type serverDocument struct {
	Issuer                 string   `json:"issuer"`
	AuthorizationEndpoint  string   `json:"authorization_endpoint"`
	TokenEndpoint          string   `json:"token_endpoint"`
	RevocationEndpoint     string   `json:"revocation_endpoint"`
	ChallengeMethods       []string `json:"code_challenge_methods_supported"`
	IssuerParameter        bool     `json:"authorization_response_iss_parameter_supported"`
	MetadataDocumentClient bool     `json:"client_id_metadata_document_supported"`
}

// document reads origin's metadata: no credential, one bounded request.
func (o OAuth) document(ctx context.Context, origin string) (serverDocument, error) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+metadataPath, nil)
	if err != nil {
		return serverDocument{}, err
	}
	request.Header.Set("Accept", "application/json")
	status, body, err := o.request(ctx, origin, request)
	if err != nil {
		return serverDocument{}, err
	}
	var document serverDocument
	if status != http.StatusOK || json.Unmarshal(body, &document) != nil {
		return serverDocument{}, &Refused{Message: noOAuth}
	}
	return document, nil
}

// namesAnother is the one refusal of a typed address whose metadata names
// an issuer elsewhere that could not be confirmed as the same server.
func namesAnother(issuer string) error {
	return &Refused{Message: "This server's sign-in names another address (" + plain(issuer) + "), which could not be confirmed as the same server"}
}

// ownServer is the server a typed address belongs to, by its metadata,
// and that server's own metadata.
//
// People type their mail host; a Stalwart server reachable under several
// names builds its metadata from the one it calls itself, so the typed
// name's issuer may be another origin. RFC 8414 3.3 forbids using metadata
// whose issuer is not the address it was read from, and that stands: the
// typed name's endpoints are never used. Instead the issuer's own metadata
// is read, once, and it is used only if it names itself. Every request of
// the sign-in then goes to the issuer, so no code, token or password is
// ever sent to a host that did not issue it.
//
// One hop only, and only when the issuer differs (the usual case costs
// the one request it always did). An issuer that names a third address (a
// chain) or the typed one back (a loop) does not name itself, and is
// refused by that one rule; so is one that is not https. The hop is made
// through the same Network as every request, so a private or internal
// issuer is refused as a typed one would be.
func (o OAuth) ownServer(ctx context.Context, typed string) (string, serverDocument, error) {
	document, err := o.document(ctx, typed)
	if err != nil {
		return "", serverDocument{}, err
	}
	server, err := jmapmail.Origin(document.Issuer)
	if err != nil {
		return "", serverDocument{}, &Refused{Message: noOAuth}
	}
	if server != typed {
		if !strings.HasPrefix(server, "https://") {
			return "", serverDocument{}, namesAnother(document.Issuer)
		}
		named := document.Issuer
		if document, err = o.document(ctx, server); err != nil {
			return "", serverDocument{}, err
		}
		if itself, err := jmapmail.Origin(document.Issuer); err != nil || itself != server {
			return "", serverDocument{}, namesAnother(named)
		}
	}
	// A server whose sign-in and token endpoints are elsewhere is not one
	// whose endpoints this app trusts, whichever address was typed.
	for _, endpoint := range []string{document.AuthorizationEndpoint, document.TokenEndpoint} {
		if got, err := jmapmail.Origin(endpoint); err != nil || got != server {
			return "", serverDocument{}, &Refused{Message: noOAuth}
		}
	}
	return server, document, nil
}

// Discover reads the server's authorization server metadata and checks it
// is one this app can use: PKCE with S256, client metadata documents, and
// every endpoint on the server's own origin, which is also its issuer.
// Metadata.Server is that origin, which may not be the one typed
// (ownServer); the caller makes the whole sign-in with it.
func (o OAuth) Discover(ctx context.Context, serverURL string) (Metadata, error) {
	origin, err := jmapmail.Origin(serverURL)
	if err != nil {
		return Metadata{}, &Refused{Message: noOAuth}
	}
	server, document, err := o.ownServer(ctx, origin)
	if err != nil {
		return Metadata{}, err
	}
	// Checked on the document of the server that will be used: after a
	// hop, the typed name's claims say nothing.
	if !slices.Contains(document.ChallengeMethods, "S256") || !document.MetadataDocumentClient {
		return Metadata{}, &Refused{Message: noOAuth}
	}
	revocation := ""
	if got, err := jmapmail.Origin(document.RevocationEndpoint); err == nil && got == server {
		revocation = document.RevocationEndpoint
	}
	return Metadata{Server: server, Issuer: document.Issuer, AuthorizationEndpoint: document.AuthorizationEndpoint,
		TokenEndpoint: document.TokenEndpoint, RevocationEndpoint: revocation, IssuerInResponse: document.IssuerParameter}, nil
}

// OwnAddress is the origin the server at serverURL calls itself, by the
// same reading of its metadata as Discover, with no credential sent: the
// typed origin, or the issuer it names that confirms itself. It does not
// ask whether this app may sign in there; an app password needs only to
// know which server it is talking to (Mail.ConnectAppPassword).
func (o OAuth) OwnAddress(ctx context.Context, serverURL string) (string, error) {
	origin, err := jmapmail.Origin(serverURL)
	if err != nil {
		return "", &Refused{Message: noOAuth}
	}
	server, _, err := o.ownServer(ctx, origin)
	return server, err
}

// plain is a value from a server bounded and stripped for a message.
func plain(text string) string {
	cleaned := strings.Join(strings.FieldsFunc(text, func(r rune) bool { return r < ' ' || r == 0x7f || r == ' ' }), " ")
	if runes := []rune(cleaned); len(runes) > 120 {
		cleaned = string(runes[:120]) + "…"
	}
	return cleaned
}

// AuthorizationRequest is RFC 6749's query for the server's sign-in page.
func AuthorizationRequest(metadata Metadata, clientID, redirectURI, state, challenge, loginHint string) string {
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {MailScope},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	if loginHint != "" {
		query.Set("login_hint", loginHint)
	}
	separator := "?"
	if strings.Contains(metadata.AuthorizationEndpoint, "?") {
		separator = "&"
	}
	return metadata.AuthorizationEndpoint + separator + query.Encode()
}

// Preflight asks the server, before the person is sent there, whether it
// accepts this app's client id and redirect URI. Stalwart answers an
// authorization request it does not trust (a client whose metadata
// documents it does not fetch: the default) with an error page and no way
// back here; its sign-in page's own client lookup (GET /api/auth/client
// with the same query) says so in JSON, which is asked here so the person
// is told on this site what to do instead.
//
// An answer that is not that lookup's (another JMAP server, an older
// Stalwart) is not a refusal: the person is sent on, as without this.
func (o OAuth) Preflight(ctx context.Context, serverURL, authorizationURL string) error {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	origin, err := jmapmail.Origin(serverURL)
	if err != nil {
		return &Refused{Message: noOAuth}
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		return &Refused{Message: noOAuth}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/auth/client?"+parsed.RawQuery, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	status, body, err := o.request(ctx, origin, request)
	if err != nil {
		return err
	}
	var answer struct {
		Error       string `json:"error"`
		Description string `json:"description"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &answer) != nil || answer.Error == "" {
		return nil
	}
	if answer.Error == "invalid_client" {
		return &Refused{Message: "This Stalwart server does not accept this app's sign-in yet (" + plain(answer.Description) +
			"). Its administrator can allow it under Settings, Authentication, OIDC Provider, client metadata documents; or connect with an app password."}
	}
	return &Refused{Message: "This server refused the sign-in request: " + plain(answer.Description)}
}

// Grant is what the token endpoint gave.
type Grant struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
}

// HasMail reports whether the grant carries the mail scope: a person can
// narrow the scopes on the consent page.
func (g Grant) HasMail() bool { return slices.Contains(strings.Fields(g.Scope), MailScope) }

// token posts a form to the token endpoint.
func (o OAuth) token(ctx context.Context, tokenEndpoint string, form url.Values, now time.Time) (Grant, error) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	origin, err := jmapmail.Origin(tokenEndpoint)
	if err != nil {
		return Grant{}, &Refused{Message: oauthRejected}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Grant{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	status, body, err := o.request(ctx, origin, request)
	if err != nil {
		return Grant{}, err
	}
	var answer struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
	}
	_ = json.Unmarshal(body, &answer)
	switch {
	case answer.Error == "invalid_grant" || answer.Error == "invalid_client" || answer.Error == "unauthorized_client":
		return Grant{}, ErrGrantRefused
	case status != http.StatusOK || answer.AccessToken == "":
		return Grant{}, &Refused{Message: "The mail server's sign-in did not answer with a token"}
	}
	expires := answer.ExpiresIn
	if expires <= 0 {
		// RFC 6749 leaves expires_in optional; Stalwart always sends it. A
		// token whose life is not said is used once and then renewed.
		expires = int64(refreshMargin / time.Second)
	}
	return Grant{AccessToken: answer.AccessToken, RefreshToken: answer.RefreshToken,
		ExpiresAt: now.Add(time.Duration(expires) * time.Second), Scope: answer.Scope}, nil
}

// Exchange trades the callback's code for a grant.
func (o OAuth) Exchange(ctx context.Context, tokenEndpoint, clientID, redirectURI, code, verifier string, now time.Time) (Grant, error) {
	return o.token(ctx, tokenEndpoint, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {clientID}, "code_verifier": {verifier},
	}, now)
}

// refresh trades a refresh token for a new pair. Stalwart replaces the
// refresh token every time; the new one must be stored before it is used.
func (o OAuth) refresh(ctx context.Context, tokenEndpoint, clientID, refreshToken string, now time.Time) (Grant, error) {
	return o.token(ctx, tokenEndpoint, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {clientID},
	}, now)
}

// Revoke ends a grant at the server (RFC 7009): Stalwart deletes the whole
// grant for either of its tokens. It reports whether the server confirmed.
func (o OAuth) Revoke(ctx context.Context, revocationEndpoint, clientID, token string) bool {
	if revocationEndpoint == "" || token == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	origin, err := jmapmail.Origin(revocationEndpoint)
	if err != nil {
		return false
	}
	form := url.Values{"token": {token}, "token_type_hint": {"refresh_token"}, "client_id": {clientID}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, revocationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return false
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	status, _, err := o.request(ctx, origin, request)
	return err == nil && status == http.StatusOK
}
