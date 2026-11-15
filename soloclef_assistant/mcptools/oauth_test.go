package mcptools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
)

// authority is a server and its authorization server on one TLS test
// server, and what each endpoint was sent.
type authority struct {
	*httptest.Server
	mu        sync.Mutex
	forms     map[string]url.Values
	basic     map[string]string
	registers int
	// challengeMetadata, when set, is the resource_metadata the server's
	// 401 names; otherwise the 401 names none and discovery reads the
	// well-known address.
	challengeMetadata bool
	// refreshStatus answers a refresh instead of granting one.
	refreshStatus int
	rotations     int
}

func newAuthority(t *testing.T) *authority {
	t.Helper()
	a := &authority{forms: map[string]url.Values{}, basic: map[string]string{}}
	mux := http.NewServeMux()
	issuer := func() string { return a.URL + "/tenant" }
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, r *http.Request) {
		challenge := `Bearer realm="mcp"`
		if a.challengeMetadata {
			challenge += `, resource_metadata="` + a.URL + `/meta/prm", scope="files:read"`
		}
		w.Header().Set("WWW-Authenticate", challenge)
		w.WriteHeader(http.StatusUnauthorized)
	})
	resource := func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource": a.URL + "/mcp", "authorization_servers": []string{issuer()}, "scopes_supported": []string{"a", "b"},
		})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", resource)
	mux.HandleFunc("GET /meta/prm", resource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/tenant", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": issuer(), "authorization_endpoint": a.URL + "/authorize", "token_endpoint": a.URL + "/token",
			"registration_endpoint": a.URL + "/register", "revocation_endpoint": a.URL + "/revoke",
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		var asked map[string]any
		_ = json.NewDecoder(r.Body).Decode(&asked)
		a.mu.Lock()
		a.registers++
		a.mu.Unlock()
		if asked["token_endpoint_auth_method"] != "none" || len(asked["redirect_uris"].([]any)) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"client_id":"registered-1"}`)
	})
	record := func(name string, r *http.Request) url.Values {
		_ = r.ParseForm()
		a.mu.Lock()
		defer a.mu.Unlock()
		a.forms[name] = r.PostForm
		a.basic[name] = r.Header.Get("Authorization")
		return r.PostForm
	}
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		form := record("token:"+r.FormValue("grant_type"), r)
		w.Header().Set("Content-Type", "application/json")
		switch form.Get("grant_type") {
		case "authorization_code":
			if form.Get("code") != "the-code" || form.Get("code_verifier") == "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"at-1","token_type":"Bearer","refresh_token":"rt-1","expires_in":"3600"}`)
		case "refresh_token":
			if a.refreshStatus != 0 {
				w.WriteHeader(a.refreshStatus)
				_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"revoked"}`)
				return
			}
			a.mu.Lock()
			a.rotations++
			a.mu.Unlock()
			_, _ = io.WriteString(w, `{"access_token":"at-2","token_type":"bearer","expires_in":3600}`)
		}
	})
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		record("revoke:"+r.FormValue("token_type_hint"), r)
	})
	a.Server = httptest.NewTLSServer(mux)
	t.Cleanup(a.Close)
	return a
}

// tlsLoopback is a Network to the test's TLS server: its certificate is
// trusted and, as the guard does, no redirect is followed.
type tlsLoopback struct{ server *httptest.Server }

func (n tlsLoopback) Client(string, []string) (*http.Client, error) {
	client := n.server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client, nil
}

func everywhere(string) []string { return nil }

func TestDiscoveryFollowsTheSpecificationsOrder(t *testing.T) {
	a := newAuthority(t)
	oauth := OAuth{Network: tlsLoopback{a.Server}, Timeout: 5 * time.Second, ClientName: "notes"}
	for name, named := range map[string]bool{"the well-known address under the path": false, "the address the 401 names": true} {
		a.challengeMetadata = named
		found, err := oauth.Discover(context.Background(), a.URL+"/mcp", everywhere)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if found.Issuer != a.URL+"/tenant" || found.TokenEndpoint != a.URL+"/token" || found.Resource != a.URL+"/mcp" ||
			found.RegistrationEndpoint == "" || found.RevocationEndpoint == "" {
			t.Errorf("%s: %+v", name, found)
		}
		// The scope the 401 names wins over the metadata's list.
		if want := map[bool]string{false: "a b", true: "files:read"}[named]; found.Scope != want {
			t.Errorf("%s: scope = %q, want %q", name, found.Scope, want)
		}
	}
	if _, err := oauth.Discover(context.Background(), "http://plain.example/mcp", everywhere); err == nil {
		t.Error("a server that is not https was discovered")
	}
}

func TestDiscoveryRefusesWhatTheSpecificationRefuses(t *testing.T) {
	for name, metadata := range map[string]string{
		"no PKCE":         `{"issuer":"%s/tenant","authorization_endpoint":"%s/a","token_endpoint":"%s/t","code_challenge_methods_supported":["plain"]}`,
		"another issuer":  `{"issuer":"https://elsewhere.example","authorization_endpoint":"%s/a","token_endpoint":"%s/t","code_challenge_methods_supported":["S256"]}`,
		"a plain address": `{"issuer":"%s/tenant","authorization_endpoint":"http://x.example/a","token_endpoint":"%s/t","code_challenge_methods_supported":["S256"]}`,
	} {
		var server *httptest.Server
		server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/mcp":
				w.WriteHeader(http.StatusUnauthorized)
			case "/.well-known/oauth-protected-resource/mcp":
				_, _ = io.WriteString(w, `{"resource":"`+server.URL+`/mcp","authorization_servers":["`+server.URL+`/tenant"]}`)
			case "/.well-known/oauth-authorization-server/tenant":
				_, _ = io.WriteString(w, strings.ReplaceAll(metadata, "%s", server.URL))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		oauth := OAuth{Network: tlsLoopback{server}, Timeout: 5 * time.Second, ClientName: "notes"}
		var refused *Refusal
		if _, err := oauth.Discover(context.Background(), server.URL+"/mcp", everywhere); !errors.As(err, &refused) {
			t.Errorf("%s: err = %v", name, err)
		}
		server.Close()
	}
	// A server with no metadata anywhere offers no sign-in.
	bare := httptest.NewTLSServer(http.NotFoundHandler())
	defer bare.Close()
	oauth := OAuth{Network: tlsLoopback{bare}, Timeout: 5 * time.Second, ClientName: "notes"}
	if _, err := oauth.Discover(context.Background(), bare.URL+"/mcp", everywhere); !errors.Is(err, ErrNoOAuth) {
		t.Errorf("no metadata: %v", err)
	}
}

func TestRegistrationExchangeAndRevocation(t *testing.T) {
	a := newAuthority(t)
	oauth := OAuth{Network: tlsLoopback{a.Server}, Timeout: 5 * time.Second, ClientName: "notes"}
	ctx := context.Background()
	found, err := oauth.Discover(ctx, a.URL+"/mcp", everywhere)
	if err != nil {
		t.Fatal(err)
	}
	client, err := oauth.Register(ctx, found, "https://notes.example/api/v1/connectors/mcp/oauth/callback", everywhere)
	if err != nil || client.ID != "registered-1" || client.AuthMethod != "none" {
		t.Fatalf("client = %+v, %v", client, err)
	}

	pkce, err := NewPKCE()
	if err != nil || len(pkce.Verifier) < 43 || pkce.Challenge == pkce.Verifier {
		t.Fatalf("pkce = %+v, %v", pkce, err)
	}
	page, err := AuthorizationURL(found.AuthorizationEndpoint, client.ID, "https://notes.example/cb", "st", pkce.Challenge, found.Resource, found.Scope)
	if err != nil {
		t.Fatal(err)
	}
	asked, _ := url.Parse(page)
	query := asked.Query()
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") != pkce.Challenge ||
		query.Get("resource") != a.URL+"/mcp" || query.Get("state") != "st" || query.Get("response_type") != "code" {
		t.Errorf("authorization URL = %s", page)
	}

	granted, err := oauth.Exchange(ctx, found.TokenEndpoint, client, "the-code", pkce.Verifier, "https://notes.example/cb", found.Resource, everywhere)
	if err != nil || granted.AccessToken != "at-1" || granted.RefreshToken != "rt-1" || granted.ExpiresIn != 3600 {
		t.Fatalf("granted = %+v, %v", granted, err)
	}
	if form := a.forms["token:authorization_code"]; form.Get("client_id") != "registered-1" || form.Get("resource") != found.Resource {
		t.Errorf("exchange form = %v", form)
	}
	if _, err := oauth.Exchange(ctx, found.TokenEndpoint, client, "a-spent-code", pkce.Verifier, "https://notes.example/cb", found.Resource, everywhere); !IsInvalidGrant(err) {
		t.Errorf("a spent code: %v", err)
	}

	// A confidential client presents its secret with HTTP Basic.
	confidential := Client{ID: "id:1", Secret: "s&2", AuthMethod: "client_secret_basic"}
	if err := oauth.Revoke(ctx, found.RevocationEndpoint, confidential, "rt-1", "refresh_token", everywhere); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("id%3A1:s%262"))
	if a.basic["revoke:refresh_token"] != want || a.forms["revoke:refresh_token"].Get("token") != "rt-1" {
		t.Errorf("revocation sent %q with %v", a.basic["revoke:refresh_token"], a.forms["revoke:refresh_token"])
	}
	for secret, wantMethod := range map[string]string{"": "none", "s": "client_secret_basic"} {
		if method, _ := ClientAuthFor(secret, nil); method != wantMethod {
			t.Errorf("ClientAuthFor(%q) = %s", secret, method)
		}
	}
	if method, _ := ClientAuthFor("s", []string{"client_secret_post"}); method != "client_secret_post" {
		t.Errorf("a server listing only the form body: %s", method)
	}
	if _, err := ClientAuthFor("s", []string{"private_key_jwt"}); err == nil {
		t.Error("a method this deployment does not speak was chosen")
	}
}

// signedIn is an OAuth server's sealed settings, signed in with tokens that
// run out at expiresAt.
func signedIn(t *testing.T, a *authority, owner string, expiresAt int64) mcpserver.Stored {
	t.Helper()
	tokens, err := mcpserver.SealTokens(testSeal, owner, mcpserver.Tokens{AccessToken: "at-1", RefreshToken: "rt-1"})
	if err != nil {
		t.Fatal(err)
	}
	return mcpserver.Stored{URL: a.URL + "/mcp", Transport: mcpserver.StreamableHTTP, Auth: mcpserver.StoredAuth{Type: mcpserver.AuthOAuth,
		OAuth: &mcpserver.StoredOAuth{ClientID: "registered-1", ClientAuth: "none", DynamicRegistration: true,
			Issuer: a.URL + "/tenant", TokenEndpoint: a.URL + "/token", Resource: a.URL + "/mcp", Tokens: tokens, ExpiresAt: expiresAt}}}
}

func storedRow(t *testing.T, rows *memoryRows, owner string, id int64, stored mcpserver.Stored) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"server_config": stored, "server_origin": "member"})
	rows.mu.Lock()
	defer rows.mu.Unlock()
	if rows.connections[owner] == nil {
		rows.connections[owner] = map[int64]Connection{}
	}
	rows.connections[owner][id] = Connection{ID: id, Name: "OAuth", Config: raw}
}

func TestAnExpiredAccessTokenIsRenewedOnceAndStored(t *testing.T) {
	a := newAuthority(t)
	rows := newRows()
	now := time.Now().Unix()
	storedRow(t, rows, ada, 7, signedIn(t, a, ada, now+10))
	renewal := TokenRenewal{OAuth: OAuth{Network: tlsLoopback{a.Server}, Timeout: 5 * time.Second, ClientName: "notes"},
		Seal: testSeal, Rows: rows, Now: now, Hosts: everywhere}
	ctx := context.Background()

	row, _ := rows.Connection(ctx, ada, 7)
	stored, _, _ := readServer(row.Config)
	tokens, err := FreshTokens(ctx, renewal, ada, 7, stored.Auth.OAuth)
	if err != nil || tokens.AccessToken != "at-2" || tokens.RefreshToken != "rt-1" {
		t.Fatalf("tokens = %+v, %v", tokens, err)
	}
	// The renewal is stored: the next read needs none.
	row, _ = rows.Connection(ctx, ada, 7)
	stored, _, _ = readServer(row.Config)
	if stored.Auth.OAuth.ExpiresAt != now+3600 {
		t.Errorf("expires_at = %d", stored.Auth.OAuth.ExpiresAt)
	}
	again, err := FreshTokens(ctx, renewal, ada, 7, stored.Auth.OAuth)
	if err != nil || again.AccessToken != "at-2" || a.rotations != 1 {
		t.Fatalf("again = %+v, %v after %d renewals", again, err, a.rotations)
	}

	// A process that renewed from a stale read loses the race and uses
	// what the winner stored, renewing nothing more.
	stale := signedIn(t, a, ada, now+10)
	if tokens, err := FreshTokens(ctx, renewal, ada, 7, stale.Auth.OAuth); err != nil || tokens.AccessToken != "at-2" {
		t.Fatalf("the losing renewal: %+v, %v", tokens, err)
	}

	// A refresh token the server no longer takes ends the sign-in.
	a.refreshStatus = http.StatusBadRequest
	storedRow(t, rows, ada, 8, signedIn(t, a, ada, now-1))
	row, _ = rows.Connection(ctx, ada, 8)
	stored, _, _ = readServer(row.Config)
	if _, err := FreshTokens(ctx, renewal, ada, 8, stored.Auth.OAuth); !IsSignInNeeded(err) {
		t.Errorf("a revoked refresh token: %v", err)
	}
	// And a server nobody signed in to has nothing to renew.
	if _, err := FreshTokens(ctx, renewal, ada, 9, &mcpserver.StoredOAuth{}); !IsSignInNeeded(err) {
		t.Errorf("no sign-in: %v", err)
	}
}

// A chat call to an OAuth server carries the account's access token, and
// one whose sign-in has ended tells the model to have the person sign in.
func TestACallCarriesTheSignInAndSaysWhenItHasEnded(t *testing.T) {
	server := newRemote(t, 0)
	server.add("lookup", says("found"))
	rows := newRows()
	tokens, _ := mcpserver.SealTokens(testSeal, ada, mcpserver.Tokens{AccessToken: "fake-access-token"})
	stored := mcpserver.Stored{URL: server.URL, Transport: mcpserver.StreamableHTTP, Auth: mcpserver.StoredAuth{Type: mcpserver.AuthOAuth,
		OAuth: &mcpserver.StoredOAuth{ClientID: "c", Tokens: tokens}}}
	raw, _ := json.Marshal(map[string]any{"server_config": stored, "server_origin": "member"})
	rows.add(ada, 1, "Files", string(raw))
	w := newWorld(t, rows, newCounter(), nil)
	set := w.turn(t, ada)
	if out, err := call(t, set, "lookup", `{}`); err != nil || out != "found" {
		t.Fatalf("call = %q, %v", out, err)
	}
	if !server.sent("Authorization", "Bearer fake-access-token") {
		t.Error("the access token was not sent")
	}

	stored.Auth.OAuth.Tokens = ""
	raw, _ = json.Marshal(map[string]any{"server_config": stored, "server_origin": "member"})
	rows.add(ada, 1, "Files", string(raw))
	if _, err := call(t, set, "lookup", `{}`); err == nil || err.Error() != signInNeeded {
		t.Errorf("after the sign-in ended: %v", err)
	}
}

// A server saved as SSE is spoken to over SSE: the transport is the row's,
// not always Streamable HTTP.
func TestAnSSEServerIsSpokenToOverSSE(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "sse-under-test", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "echo", Description: "Echoes.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "over sse"}}}, nil
		})
	var sawKey bool
	handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server }, nil)
	listening := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = sawKey || r.Header.Get("x-api-key") == "fake-sse-key"
		handler.ServeHTTP(w, r)
	}))
	defer listening.Close()
	rows := newRows()
	rows.add(ada, 1, "Old", `{"server_config": {"transport": "sse", "url": "`+listening.URL+`", "headers": {"x-api-key": "fake-sse-key"}}, "server_origin": "member"}`)
	w := newWorld(t, rows, newCounter(), nil)
	set := w.turn(t, ada)
	if out, err := call(t, set, "echo", `{}`); err != nil || out != "over sse" {
		t.Fatalf("call = %q, %v", out, err)
	}
	if !sawKey {
		t.Error("the SSE connection did not carry the key")
	}
}

// Removing a signed-in server revokes both its tokens at the
// authorization server; a server with no sign-in has nothing to revoke.
func TestSignOutRevokesBothTokens(t *testing.T) {
	a := newAuthority(t)
	stored := signedIn(t, a, ada, 0)
	stored.Auth.OAuth.RevocationEndpoint = a.URL + "/revoke"
	config, _ := json.Marshal(map[string]any{"server_config": stored, "server_origin": "member"})
	signOut := SignOut{OAuth: OAuth{Network: tlsLoopback{a.Server}, Timeout: 5 * time.Second, ClientName: "notes"}, Seal: testSeal,
		SiteHosts: func(context.Context) ([]string, error) { return nil, errors.New("not asked for a member's server") }}
	if outcome := signOut.RevokeServer(context.Background(), ada, config); outcome != SignOutRevoked {
		t.Fatalf("outcome = %s", outcome)
	}
	if a.forms["revoke:refresh_token"].Get("token") != "rt-1" || a.forms["revoke:access_token"].Get("token") != "at-1" {
		t.Errorf("revoked %v and %v", a.forms["revoke:refresh_token"], a.forms["revoke:access_token"])
	}
	plain, _ := json.Marshal(map[string]any{"server_config": mcpserver.Stored{URL: a.URL, Transport: mcpserver.SSE, Auth: mcpserver.StoredAuth{Type: mcpserver.AuthNone}}})
	if outcome := signOut.RevokeServer(context.Background(), ada, plain); outcome != SignOutNothing {
		t.Errorf("a server with no sign-in: %s", outcome)
	}
	if outcome := signOut.RevokeServer(context.Background(), ben, config); outcome == SignOutRevoked {
		t.Error("another account's tokens were opened")
	}
}
