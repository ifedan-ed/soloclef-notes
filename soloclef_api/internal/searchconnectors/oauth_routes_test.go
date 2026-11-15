package searchconnectors

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// authority is an MCP server and its authorization server on one TLS test
// server, recording what its token and revocation endpoints were sent.
type authority struct {
	*httptest.Server
	mu    sync.Mutex
	forms map[string]url.Values
}

func newAuthority(t *testing.T) *authority {
	t.Helper()
	a := &authority{forms: map[string]url.Values{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": a.URL + "/mcp", "authorization_servers": []string{a.URL}})
	})
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": a.URL, "authorization_endpoint": a.URL + "/authorize",
			"token_endpoint": a.URL + "/token", "registration_endpoint": a.URL + "/register", "revocation_endpoint": a.URL + "/revoke",
			"code_challenge_methods_supported": []string{"S256"}})
	})
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"client_id":"registered-1"}`)
	})
	record := func(name string, r *http.Request) url.Values {
		_ = r.ParseForm()
		a.mu.Lock()
		defer a.mu.Unlock()
		a.forms[name] = r.PostForm
		return r.PostForm
	}
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		form := record("token", r)
		if form.Get("code") != "the-code" || form.Get("code_verifier") == "" || form.Get("client_id") != "registered-1" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"fake-access-1","token_type":"Bearer","refresh_token":"fake-refresh-1","expires_in":3600}`)
	})
	mux.HandleFunc("POST /revoke", func(_ http.ResponseWriter, r *http.Request) { record("revoke:"+r.FormValue("token_type_hint"), r) })
	a.Server = httptest.NewTLSServer(mux)
	t.Cleanup(a.Close)
	return a
}

// tlsNetwork reaches the test's TLS server, trusting its certificate and,
// as the guard does, following no redirect.
type tlsNetwork struct{ server *httptest.Server }

func (n tlsNetwork) Client(string, []string) (*http.Client, error) {
	client := n.server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client, nil
}

// signOutRevoker is the delete route's Revoker over the real MCP sign-out.
type signOutRevoker struct{ signOut mcptools.SignOut }

func (r signOutRevoker) Revoke(ctx context.Context, ownerID string, _ int64, _ string, config []byte) {
	r.signOut.RevokeServer(ctx, ownerID, config)
}

const testRedirect = "https://notes.example/api/v1/connectors/mcp/oauth/callback"

func TestSigningInToAnOAuthServerFromStartToRevocation(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	a := newAuthority(t)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	store := Store{Pool: pool}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	policy := ServerPolicy{Standing: store, SiteHostsWhenUnset: []string{}, Destinations: fakeInternet{}, Seal: testServerSeal}
	oauth := mcptools.OAuth{Network: tlsNetwork{a.Server}, Timeout: 5 * time.Second, ClientName: "notes"}
	attempts, _ := sealedsecret.New("fake-deployment-secret", sealedsecret.MCPOAuthAttempt)
	probe := &fakeProbe{}
	tools := &bumps{}
	routes := OAuthRoutes{Rows: store, Writes: store, Policy: policy, OAuth: oauth, Attempts: attempts,
		Settings: OAuthSettings{RedirectURI: testRedirect, FrontendURL: "https://notes.example/"},
		Tools:    tools, Now: time.Now, Errors: envelope, Logger: logger}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	ServerTestRoute{Rows: store, Policy: policy, Probe: probe, OAuth: oauth, Tokens: mcptools.Postgres{Pool: pool},
		Now: time.Now, Errors: envelope, Logger: logger}.Register(api)
	ServerDeleteRoute{Changes: store, Revocation: signOutRevoker{mcptools.SignOut{OAuth: oauth, Seal: testServerSeal,
		SiteHosts: func(context.Context) ([]string, error) { return nil, nil }}}, Tools: tools, Errors: envelope, Logger: logger}.Register(api)
	caller := alice
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != OAuthCallbackPath {
			request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: caller, Method: principal.MethodSession}))
		}
		api.Adapter().ServeHTTP(response, request)
	})
	bodies := requestbody.Middleware{Covers: requestbody.CoveringRoutes(BodyRoutes()...)}
	handler := requestcontext.Middleware(logger, bodies.Wrap(withCaller))
	send := func(method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if strings.Contains(recorder.Body.String(), "fake-") {
			t.Fatalf("%s %s answered a credential: %s", method, path, recorder.Body.String())
		}
		return recorder
	}

	// A saved OAuth server with no client typed: the provider registers one.
	stored, err := mcpserver.Build(testServerSeal, alice, mcpserver.Settings{Address: a.URL + "/mcp", Transport: mcpserver.StreamableHTTP,
		Auth: mcpserver.Auth{Kind: mcpserver.AuthOAuth}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	column, _ := json.Marshal(map[string]any{"server_config": stored, "server_origin": "member"})
	var id int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO search_source_connectors (user_id, name, connector_type, config)
		VALUES ($1, 'Files', 'MCP_CONNECTOR', $2::jsonb) RETURNING id`, alice, string(column)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	startPath := strings.Replace(OAuthStartPath, "{connector_id}", strconv.FormatInt(id, 10), 1)

	if answered := send(http.MethodPost, OAuthDiscoverPath, `{"url": "`+a.URL+`/mcp"}`).Body.String(); answered !=
		`{"status":"success","authorization_server":"`+a.URL+`","dynamic_registration":true}` {
		t.Fatalf("discover: %s", answered)
	}

	// Another account cannot start a sign-in to her server.
	caller = bob
	if code := send(http.MethodPost, startPath, `{}`).Code; code != http.StatusNotFound {
		t.Errorf("Bob's start answered %d", code)
	}
	caller = alice

	started := send(http.MethodPost, startPath, `{"return_path": "/dashboard/settings/connectors?tab=mcp"}`)
	if started.Code != http.StatusOK {
		t.Fatalf("start: %d %s", started.Code, started.Body)
	}
	var page struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	_ = json.Unmarshal(started.Body.Bytes(), &page)
	asked, _ := url.Parse(page.AuthorizationURL)
	query := asked.Query()
	if asked.Path != "/authorize" || query.Get("client_id") != "registered-1" || query.Get("code_challenge_method") != "S256" ||
		query.Get("redirect_uri") != testRedirect || query.Get("resource") != a.URL+"/mcp" || query.Get("state") == "" {
		t.Fatalf("authorization URL = %s", page.AuthorizationURL)
	}
	cookies := started.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Path != OAuthCallbackPath || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookies = %+v", cookies)
	}

	// A callback with another state, or without this browser's cookie, is
	// refused and stores nothing.
	for name, attempt := range map[string]func() *httptest.ResponseRecorder{
		"another state": func() *httptest.ResponseRecorder {
			return send(http.MethodGet, OAuthCallbackPath+"?code=the-code&state=forged", "", cookies[0])
		},
		"no cookie": func() *httptest.ResponseRecorder {
			return send(http.MethodGet, OAuthCallbackPath+"?code=the-code&state="+url.QueryEscape(query.Get("state")), "")
		},
	} {
		answered := attempt()
		if answered.Code != http.StatusSeeOther || answered.Header().Get("Location") != "https://notes.example/dashboard/settings/connectors?mcp_oauth=expired" {
			t.Errorf("%s: %d %s", name, answered.Code, answered.Header().Get("Location"))
		}
	}
	if probe.calls != 0 || a.forms["token"] != nil {
		t.Fatal("a refused callback reached the token endpoint")
	}

	back := send(http.MethodGet, OAuthCallbackPath+"?code=the-code&state="+url.QueryEscape(query.Get("state")), "", cookies[0])
	want := "https://notes.example/dashboard/settings/connectors?tab=mcp&mcp_connector=" + strconv.FormatInt(id, 10) + "&mcp_oauth=connected"
	if back.Code != http.StatusSeeOther || back.Header().Get("Location") != want {
		t.Fatalf("callback: %d %s", back.Code, back.Header().Get("Location"))
	}
	var raw string
	_ = pool.QueryRow(context.Background(), `SELECT config::text FROM search_source_connectors WHERE id = $1`, id).Scan(&raw)
	if strings.Contains(raw, "fake-access-1") || strings.Contains(raw, "fake-refresh-1") || !strings.Contains(raw, "tokens_sealed") {
		t.Fatalf("the tokens are not stored sealed: %s", raw)
	}
	if len(tools.accounts) != 1 {
		t.Errorf("the account's tools were not told: %v", tools.accounts)
	}

	// The test connects with the sign-in.
	if answered := send(http.MethodPost, ServerTestPath, `{"url": "`+a.URL+`/mcp", "transport": "streamable-http",
		"auth": {"type": "oauth"}, "connector_id": `+strconv.FormatInt(id, 10)+`}`).Body.String(); !strings.Contains(answered, `"status":"success"`) {
		t.Fatalf("test: %s", answered)
	}
	if probe.headers["Authorization"] != "Bearer fake-access-1" {
		t.Errorf("the probe was given %v", probe.headers)
	}

	// Disconnecting removes the server and revokes both tokens.
	if code := send(http.MethodDelete, strings.Replace(ServerPath, "{connector_id}", strconv.FormatInt(id, 10), 1), "").Code; code != http.StatusNoContent {
		t.Fatalf("delete answered %d", code)
	}
	if a.forms["revoke:refresh_token"].Get("token") != "fake-refresh-1" || a.forms["revoke:access_token"].Get("token") != "fake-access-1" {
		t.Errorf("revoked %v and %v", a.forms["revoke:refresh_token"], a.forms["revoke:access_token"])
	}
}

// With no callback address configured nothing is offered, and saying so is
// the answer, not a redirect to nowhere.
func TestASignInIsNotOfferedWithoutItsCallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	attempts, _ := sealedsecret.New("s", sealedsecret.MCPOAuthAttempt)
	routes := OAuthRoutes{Rows: Store{}, Writes: Store{}, Policy: ServerPolicy{Seal: testServerSeal},
		OAuth: mcptools.OAuth{Network: tlsNetwork{}, Timeout: time.Second, ClientName: "n"}, Attempts: attempts,
		Tools: &bumps{}, Now: time.Now, Errors: errorenvelope.Writer{}, Logger: logger}
	if err := routes.Check(); err != nil {
		t.Fatalf("unconfigured is a valid deployment: %v", err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, OAuthCallbackPath+"?code=c&state=s", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), oauthNotConfigured) {
		t.Errorf("answered %d %s", recorder.Code, recorder.Body)
	}
	for _, wrong := range []OAuthSettings{
		{RedirectURI: "https://notes.example/elsewhere", FrontendURL: "https://notes.example"},
		{RedirectURI: testRedirect},
		{RedirectURI: "notes.example" + OAuthCallbackPath, FrontendURL: "https://notes.example"},
	} {
		routes.Settings = wrong
		if routes.Check() == nil {
			t.Errorf("%+v was accepted", wrong)
		}
	}
	if safeReturnPath("//evil.example/x") != "" {
		t.Error("a scheme-relative return path was kept")
	}
}
