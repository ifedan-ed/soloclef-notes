package mcptools

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// This file is the OAuth side of the MCP authorization specification
// (2025-11-25): how this deployment finds a server's authorization server,
// registers itself there, sends a person to sign in, and exchanges, renews
// and revokes the tokens.
//
// Every request goes through the Network, the same guard a connection to
// the server goes through, with the hosts the caller says the URL may
// reach. A server's metadata names the authorization server and the
// authorization server's metadata names its endpoints; both are documents a
// remote party wrote, so without the guard either could point this process
// at an address inside its own network. No redirect is followed.
//
// What is deliberately not here: no implicit or password grant, no client
// credentials grant, no OpenID Connect. PKCE with S256 is required and an
// authorization server that does not advertise it is refused, as the
// specification says a client must.

// OAuth speaks to authorization servers. All fields are required.
type OAuth struct {
	Network Network
	// Timeout bounds one request.
	Timeout time.Duration
	// ClientName is the name this deployment registers itself under.
	ClientName string
}

// Check reports what is missing. Called once at start-up.
func (o OAuth) Check() error {
	switch {
	case o.Network == nil:
		return errors.New("mcptools: OAuth.Network is not set")
	case o.Timeout <= 0:
		return errors.New("mcptools: OAuth.Timeout must be positive")
	case strings.TrimSpace(o.ClientName) == "":
		return errors.New("mcptools: OAuth.ClientName is not set")
	}
	return nil
}

// HostsFor is the hosts a request to address may reach: for a person's own
// server, the address's own public host; for a site server, the site's
// allowed hosts (OAuthHosts).
type HostsFor func(address string) []string

// OAuthHosts is the HostsFor of a server of this origin: the rule a
// connection to the server is held to, applied to each OAuth address in
// turn.
func OAuthHosts(origin string, siteHosts []string) HostsFor {
	return func(address string) []string { return reachableHosts(origin, address, siteHosts) }
}

// Discovered is what discovery found: the resource a token is to be issued
// for and the authorization server's endpoints.
type Discovered struct {
	// Resource is the server's canonical address as its metadata states
	// it, sent as RFC 8707's `resource` with every authorization and token
	// request.
	Resource string
	Issuer   string
	// The endpoints. Registration and revocation are optional.
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	RevocationEndpoint    string
	// Scope is what to ask for: the scope the server's 401 named, or the
	// scopes its metadata lists, space-separated; "" for none.
	Scope string
	// AuthMethods is token_endpoint_auth_methods_supported, as listed.
	AuthMethods []string
}

// ErrNoOAuth is a server that does not offer an OAuth sign-in: no protected
// resource metadata at any of the places the specification names.
var ErrNoOAuth = errors.New("mcptools: the server does not offer an OAuth sign-in")

// OAuthError is an authorization server's refusal. Code is its `error`
// word; Description is its own text, for the log only.
type OAuthError struct {
	Status      int
	Code        string
	Description string
}

func (e *OAuthError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("the authorization server refused (%d %s)", e.Status, e.Code)
	}
	return fmt.Sprintf("the authorization server refused (%d)", e.Status)
}

// IsInvalidGrant reports whether err is the server saying a code or a
// refresh token is no longer good: the person must sign in again.
func IsInvalidGrant(err error) bool {
	var refused *OAuthError
	return errors.As(err, &refused) && refused.Code == "invalid_grant"
}

// metadataLimit is the most of a metadata document or token answer read.
// Each is a few kilobytes; a megabyte is a wrong answer.
const metadataLimit = 1 << 20

// resourceMetadataParameter finds resource_metadata="…" in a 401's
// WWW-Authenticate header (RFC 9728 §5.1), and scopeParameter its scope.
var (
	resourceMetadataParameter = regexp.MustCompile(`resource_metadata="([^"]+)"`)
	scopeParameter            = regexp.MustCompile(`scope="([^"]*)"`)
)

// Discover finds serverURL's authorization server, in the order the
// specification gives (each step is the specification's, not a guess):
//
//  1. ask the server itself without a token; a 401 names its resource
//     metadata in WWW-Authenticate;
//  2. otherwise the metadata at the well-known address under the server's
//     path (RFC 9728 §3.1), then at the host's root;
//  3. the authorization server's own metadata at its RFC 8414 address,
//     whose issuer must be exactly the one the resource named.
func (o OAuth) Discover(ctx context.Context, serverURL string, hosts HostsFor) (Discovered, error) {
	server, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || server.Scheme != "https" || server.Host == "" {
		return Discovered{}, refuse("the server address is not an https URL")
	}

	var candidates []string
	headerScope := ""
	if named, scope, err := o.challenge(ctx, server.String(), hosts); err != nil {
		return Discovered{}, err
	} else if named != "" {
		candidates, headerScope = []string{named}, scope
	}
	path := strings.TrimSuffix(server.EscapedPath(), "/")
	root := server.Scheme + "://" + server.Host + "/.well-known/oauth-protected-resource"
	if path != "" {
		candidates = append(candidates, root+path)
	}
	candidates = append(candidates, root)

	var resource struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	found := false
	for _, candidate := range candidates {
		status, err := o.getJSON(ctx, candidate, hosts, &resource)
		if err != nil {
			return Discovered{}, err
		}
		if status == http.StatusOK {
			found = true
			break
		}
	}
	if !found || len(resource.AuthorizationServers) == 0 {
		return Discovered{}, ErrNoOAuth
	}
	// The metadata must be about this server: a document on the server's
	// host that names another origin is not one this deployment may act on.
	canonical := resource.Resource
	if canonical == "" {
		canonical = server.String()
	}
	if stated, err := url.Parse(canonical); err != nil || !strings.EqualFold(stated.Host, server.Host) || stated.Scheme != "https" {
		return Discovered{}, refuse("the server's metadata describes another resource")
	}

	issuer := resource.AuthorizationServers[0]
	authority, err := url.Parse(issuer)
	if err != nil || authority.Scheme != "https" || authority.Host == "" || authority.RawQuery != "" || authority.Fragment != "" {
		return Discovered{}, refuse("the authorization server is not an https URL")
	}
	metadataURL := authority.Scheme + "://" + authority.Host + "/.well-known/oauth-authorization-server" + strings.TrimSuffix(authority.EscapedPath(), "/")
	var metadata struct {
		Issuer                 string   `json:"issuer"`
		AuthorizationEndpoint  string   `json:"authorization_endpoint"`
		TokenEndpoint          string   `json:"token_endpoint"`
		RegistrationEndpoint   string   `json:"registration_endpoint"`
		RevocationEndpoint     string   `json:"revocation_endpoint"`
		CodeChallengeMethods   []string `json:"code_challenge_methods_supported"`
		TokenEndpointAuthMeths []string `json:"token_endpoint_auth_methods_supported"`
	}
	status, err := o.getJSON(ctx, metadataURL, hosts, &metadata)
	if err != nil {
		return Discovered{}, err
	}
	if status != http.StatusOK {
		return Discovered{}, fmt.Errorf("the authorization server's metadata answered %d", status)
	}
	// RFC 8414 §3.3: the issuer in the document must be the one it was
	// fetched for, or a document on one host could speak for another.
	if strings.TrimSuffix(metadata.Issuer, "/") != strings.TrimSuffix(issuer, "/") {
		return Discovered{}, refuse("the authorization server's metadata names another issuer")
	}
	if !slices.Contains(metadata.CodeChallengeMethods, "S256") {
		return Discovered{}, refuse("the authorization server does not support PKCE with S256")
	}
	for _, endpoint := range []string{metadata.AuthorizationEndpoint, metadata.TokenEndpoint} {
		if !httpsURL(endpoint) {
			return Discovered{}, refuse("the authorization server's endpoints are not https URLs")
		}
	}
	for _, endpoint := range []string{metadata.RegistrationEndpoint, metadata.RevocationEndpoint} {
		if endpoint != "" && !httpsURL(endpoint) {
			return Discovered{}, refuse("the authorization server's endpoints are not https URLs")
		}
	}
	scope := headerScope
	if scope == "" {
		scope = strings.Join(resource.ScopesSupported, " ")
	}
	return Discovered{
		Resource: canonical, Issuer: issuer,
		AuthorizationEndpoint: metadata.AuthorizationEndpoint, TokenEndpoint: metadata.TokenEndpoint,
		RegistrationEndpoint: metadata.RegistrationEndpoint, RevocationEndpoint: metadata.RevocationEndpoint,
		Scope: scope, AuthMethods: metadata.TokenEndpointAuthMeths,
	}, nil
}

// challenge asks the server to start a session with no token, which is
// how a server that wants one says where its metadata is. named is "" when
// it answered anything but a 401 naming its metadata.
func (o OAuth) challenge(ctx context.Context, serverURL string, hosts HostsFor) (named, scope string, err error) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + protocolVersion +
		`","capabilities":{},"clientInfo":{"name":` + strconv.Quote(o.ClientName) + `,"version":"` + clientVersion + `"}}}`)
	response, err := o.send(ctx, http.MethodPost, serverURL, hosts, http.Header{
		"Content-Type": {"application/json"}, "Accept": {"application/json, text/event-stream"},
	}, body)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, metadataLimit))
	if response.StatusCode != http.StatusUnauthorized {
		return "", "", nil
	}
	header := strings.Join(response.Header.Values("WWW-Authenticate"), ", ")
	if match := resourceMetadataParameter.FindStringSubmatch(header); match != nil && httpsURL(match[1]) {
		named = match[1]
	}
	if match := scopeParameter.FindStringSubmatch(header); match != nil {
		scope = match[1]
	}
	return named, scope, nil
}

// Client is this deployment as a client of one authorization server.
type Client struct {
	ID     string
	Secret string
	// AuthMethod is how the secret is presented: "none",
	// "client_secret_basic" or "client_secret_post".
	AuthMethod string
}

// ClientAuthFor is how a client typed by a person presents itself: with
// no secret, PKCE alone; with one, HTTP Basic unless the server lists only
// the form body (RFC 8414 makes Basic the default when it lists nothing).
func ClientAuthFor(secret string, supported []string) (string, error) {
	switch {
	case secret == "":
		return "none", nil
	case len(supported) == 0 || slices.Contains(supported, "client_secret_basic"):
		return "client_secret_basic", nil
	case slices.Contains(supported, "client_secret_post"):
		return "client_secret_post", nil
	}
	return "", refuse("the authorization server accepts no client secret method this deployment speaks")
}

// Register registers this deployment with the authorization server (RFC
// 7591) as a public client that uses PKCE, for one redirect address.
func (o OAuth) Register(ctx context.Context, found Discovered, redirectURI string, hosts HostsFor) (Client, error) {
	if found.RegistrationEndpoint == "" {
		return Client{}, errors.New("mcptools: the authorization server offers no dynamic registration")
	}
	request, err := json.Marshal(map[string]any{
		"client_name":                o.ClientName,
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	if err != nil {
		return Client{}, err
	}
	response, err := o.send(ctx, http.MethodPost, found.RegistrationEndpoint, hosts,
		http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}}, request)
	if err != nil {
		return Client{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, metadataLimit))
	if err != nil {
		return Client{}, err
	}
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return Client{}, oauthRefusal(response.StatusCode, raw)
	}
	var registered struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		AuthMethod   string `json:"token_endpoint_auth_method"`
	}
	if json.Unmarshal(raw, &registered) != nil || registered.ClientID == "" {
		return Client{}, errors.New("mcptools: the registration answer has no client id")
	}
	client := Client{ID: registered.ClientID, Secret: registered.ClientSecret, AuthMethod: "none"}
	if registered.ClientSecret != "" {
		// A server may issue a secret although none was asked for; it
		// then says how it wants it, and Basic is RFC 7591's default.
		client.AuthMethod = "client_secret_basic"
		if registered.AuthMethod == "client_secret_post" {
			client.AuthMethod = "client_secret_post"
		}
	}
	return client, nil
}

// PKCE is one sign-in's proof key: the verifier is kept, the challenge
// sent.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE makes a fresh verifier (RFC 7636: 43 to 128 characters; 64
// random bytes encode to 86) and its S256 challenge.
func NewPKCE() (PKCE, error) {
	raw := make([]byte, 64)
	if _, err := rand.Read(raw); err != nil {
		return PKCE{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(verifier))
	return PKCE{Verifier: verifier, Challenge: base64.RawURLEncoding.EncodeToString(digest[:])}, nil
}

// AuthorizationURL is the authorization server's page the person is sent
// to: the code flow with PKCE, the state, and the resource.
func AuthorizationURL(endpoint, clientID, redirectURI, state, challenge, resource, scope string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || !httpsURL(endpoint) {
		return "", refuse("the authorization endpoint is not an https URL")
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("resource", resource)
	if scope != "" {
		query.Set("scope", scope)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// Granted is a token endpoint's answer.
type Granted struct {
	AccessToken  string
	RefreshToken string
	// ExpiresIn is seconds from now; zero when the server did not say.
	ExpiresIn int64
	Scope     string
}

// Exchange trades an authorization code for tokens.
func (o OAuth) Exchange(ctx context.Context, tokenEndpoint string, client Client, code, verifier, redirectURI, resource string, hosts HostsFor) (Granted, error) {
	return o.token(ctx, tokenEndpoint, client, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
		"redirect_uri": {redirectURI}, "resource": {resource},
	}, hosts)
}

// Refresh trades a refresh token for a new access token. A server that
// rotates refresh tokens answers a new one; one that does not answers none,
// and the old one stays good (the caller keeps it).
func (o OAuth) Refresh(ctx context.Context, tokenEndpoint string, client Client, refreshToken, resource string, hosts HostsFor) (Granted, error) {
	return o.token(ctx, tokenEndpoint, client, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "resource": {resource},
	}, hosts)
}

func (o OAuth) token(ctx context.Context, endpoint string, client Client, form url.Values, hosts HostsFor) (Granted, error) {
	header := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Accept": {"application/json"}}
	authenticate(client, form, header)
	response, err := o.send(ctx, http.MethodPost, endpoint, hosts, header, []byte(form.Encode()))
	if err != nil {
		return Granted{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, metadataLimit))
	if err != nil {
		return Granted{}, err
	}
	if response.StatusCode != http.StatusOK {
		return Granted{}, oauthRefusal(response.StatusCode, raw)
	}
	var answer struct {
		AccessToken  string          `json:"access_token"`
		TokenType    string          `json:"token_type"`
		RefreshToken string          `json:"refresh_token"`
		ExpiresIn    json.RawMessage `json:"expires_in"`
		Scope        string          `json:"scope"`
	}
	if json.Unmarshal(raw, &answer) != nil || answer.AccessToken == "" {
		return Granted{}, errors.New("mcptools: the token answer has no access token")
	}
	// The token is sent as a bearer token and as nothing else.
	if !strings.EqualFold(answer.TokenType, "bearer") {
		return Granted{}, fmt.Errorf("mcptools: the token is of type %q, not a bearer token", answer.TokenType)
	}
	granted := Granted{AccessToken: answer.AccessToken, RefreshToken: answer.RefreshToken, Scope: answer.Scope}
	// Some servers send the number as text.
	if seconds, err := strconv.ParseInt(strings.Trim(string(answer.ExpiresIn), `"`), 10, 64); err == nil && seconds > 0 {
		granted.ExpiresIn = seconds
	}
	return granted, nil
}

// Revoke ends a token at the authorization server (RFC 7009). A server
// answers 200 for a token it did not know, too, so success says the token
// is dead either way.
func (o OAuth) Revoke(ctx context.Context, endpoint string, client Client, token, hint string, hosts HostsFor) error {
	form := url.Values{"token": {token}, "token_type_hint": {hint}}
	header := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	authenticate(client, form, header)
	response, err := o.send(ctx, http.MethodPost, endpoint, hosts, header, []byte(form.Encode()))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, metadataLimit))
	if response.StatusCode != http.StatusOK {
		return oauthRefusal(response.StatusCode, raw)
	}
	return nil
}

// authenticate puts the client's identity on a token or revocation request
// in the way it was registered to present it.
func authenticate(client Client, form url.Values, header http.Header) {
	switch client.AuthMethod {
	case "client_secret_basic":
		// RFC 6749 §2.3.1: each part form-encoded before it is joined.
		credentials := url.QueryEscape(client.ID) + ":" + url.QueryEscape(client.Secret)
		header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	case "client_secret_post":
		form.Set("client_id", client.ID)
		form.Set("client_secret", client.Secret)
	default:
		form.Set("client_id", client.ID)
	}
}

func oauthRefusal(status int, raw []byte) error {
	var said struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &said)
	return &OAuthError{Status: status, Code: said.Error, Description: said.Description}
}

// getJSON fetches a metadata document into into. A 404 or any other
// non-200 is answered as its status with no error: it is the server saying
// "not here", which discovery reads as "try the next place".
func (o OAuth) getJSON(ctx context.Context, address string, hosts HostsFor, into any) (int, error) {
	response, err := o.send(ctx, http.MethodGet, address, hosts, http.Header{"Accept": {"application/json"}}, nil)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, metadataLimit))
	if err != nil {
		return 0, err
	}
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, nil
	}
	if json.Unmarshal(raw, into) != nil {
		return 0, fmt.Errorf("mcptools: the metadata at %s is not JSON", Host(address))
	}
	return http.StatusOK, nil
}

// send makes one request through the guard, bounded by Timeout. The error
// names the host at most: an OAuth address can carry a code or a token.
func (o OAuth) send(ctx context.Context, method, address string, hosts HostsFor, header http.Header, body []byte) (*http.Response, error) {
	client, err := o.Network.Client(address, hosts(address))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		cancel()
		return nil, refuse("an OAuth address is not a valid URL")
	}
	request.Header = header
	response, err := client.Do(request)
	if err != nil {
		cancel()
		client.CloseIdleConnections()
		var refused *Refusal
		if errors.As(err, &refused) {
			return nil, refused
		}
		return nil, fmt.Errorf("mcptools: %s could not be reached", Host(address))
	}
	// The body is read by the caller; the timeout's cancel goes with it.
	response.Body = cancelOnClose{ReadCloser: response.Body, cancel: cancel, done: client.CloseIdleConnections}
	return response, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
	done   func()
}

func (c cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	c.done()
	return err
}

func httpsURL(address string) bool {
	parsed, err := url.Parse(address)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}
