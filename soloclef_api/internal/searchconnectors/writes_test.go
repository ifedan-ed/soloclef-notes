package searchconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// fakeInternet stands in for the real guard's DNS: the address's shape is
// checked by the real rules (mcptools.PublicInternet.Verify), and a name
// resolves to a public address unless it is listed as private here.
type fakeInternet struct{ private map[string]bool }

func (f fakeInternet) Verify(ctx context.Context, address string, hosts []string) error {
	guard := mcptools.PublicInternet{ConnectTimeout: time.Second,
		Resolve: func(_ context.Context, host string) ([]netip.Addr, error) {
			if f.private[host] {
				return []netip.Addr{netip.MustParseAddr("10.0.0.7")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}}
	return guard.Verify(ctx, address, hosts)
}

// fakeProbe records what a test would have connected with.
type fakeProbe struct {
	address   string
	transport string
	headers   map[string]string
	hosts     []string
	calls     int
	fail      bool
}

func (f *fakeProbe) Tools(_ context.Context, address, transport string, headers map[string]string, hosts []string) ([]mcptools.ProbedTool, error) {
	f.calls++
	f.address, f.transport, f.headers, f.hosts = address, transport, headers, hosts
	if f.fail {
		return nil, errors.New("the server did not answer")
	}
	return []mcptools.ProbedTool{{Name: "search", Description: "Search the web",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)}}, nil
}

// testServerSeal is the tests' deployment's seal for server secrets.
var testServerSeal = func() sealedsecret.Seal {
	seal, err := sealedsecret.New("fake-deployment-secret", sealedsecret.MCPServerSecret)
	if err != nil {
		panic(err)
	}
	return seal
}()

// connectorWrites is every write route of this file's subject over the real
// statements, with a caller that a test switches.
type connectorWrites struct {
	t       *testing.T
	pool    *pgxpool.Pool
	tools   *bumps
	probe   *fakeProbe
	handler http.Handler
	caller  principal.Principal
}

var fixedNow = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

func newConnectorWrites(t *testing.T) *connectorWrites {
	t.Helper()
	pool := testpostgres.Open(t, databaseVariable)
	w := &connectorWrites{t: t, pool: pool, tools: &bumps{}, probe: &fakeProbe{},
		caller: principal.Principal{UserID: testpostgres.Alice, Method: principal.MethodSession}}
	store := Store{Pool: pool}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	policy := ServerPolicy{
		Standing:           store,
		SiteHostsWhenUnset: []string{"mcp.site.example"},
		Destinations:       fakeInternet{private: map[string]bool{"inside.example.test": true}},
		Seal:               testServerSeal,
	}
	now := func() time.Time { return fixedNow }
	oauth := mcptools.OAuth{Network: mcptools.PublicInternet{Resolve: mcptools.SystemResolve, ConnectTimeout: time.Second},
		Timeout: time.Second, ClientName: "notes"}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	OneRoute{Rows: store, Errors: envelope, Logger: logger}.Register(api)
	ServerRoute{Rows: store, Errors: envelope, Logger: logger}.Register(api)
	ServerCreateRoute{Writes: store, Policy: policy, Tools: w.tools, Errors: envelope, Logger: logger}.Register(api)
	ServerUpdateRoute{Rows: store, Writes: store, Policy: policy, Tools: w.tools, Errors: envelope, Logger: logger}.Register(api)
	ServerTestRoute{Rows: store, Policy: policy, Probe: w.probe, OAuth: oauth, Tokens: mcptools.Postgres{Pool: pool},
		Now: now, Errors: envelope, Logger: logger}.Register(api)
	PolicyRoute{Policy: policy, OAuthAvailable: true, Errors: envelope, Logger: logger}.Register(api)
	ConnectionCreateRoute{Writes: store, Now: now, Errors: envelope, Logger: logger}.Register(api)
	ConnectionUpdateRoute{Rows: store, Writes: store, Policy: policy, Tools: w.tools, Now: now, Errors: envelope, Logger: logger}.Register(api)

	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		api.Adapter().ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), w.caller)))
	})
	bodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes(BodyRoutes()...),
	}
	w.handler = requestcontext.Middleware(logger, bodies.Wrap(withCaller))
	return w
}

// as sends a request as a session of userID.
func (w *connectorWrites) as(userID, method, path, body string) (int, string) {
	w.t.Helper()
	w.caller = principal.Principal{UserID: userID, Method: principal.MethodSession}
	return w.send(method, path, body)
}

func (w *connectorWrites) send(method, path, body string) (int, string) {
	w.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	w.handler.ServeHTTP(recorder, request)
	answered := recorder.Body.String()
	// The rule for every answer of every route: no stored credential, in
	// the clear or as ciphertext. Every invented one here begins "fake-",
	// and every sealed value "v1:".
	if strings.Contains(answered, "fake-") || strings.Contains(answered, `"v1:`) || strings.Contains(answered, "sealed") {
		w.t.Fatalf("%s %s answered a stored credential: %s", method, path, answered)
	}
	return recorder.Code, answered
}

// config is a connection's stored config as the database holds it.
func (w *connectorWrites) config(id int64) map[string]any {
	w.t.Helper()
	var config map[string]any
	if err := json.Unmarshal(w.rawConfig(id), &config); err != nil {
		w.t.Fatal(err)
	}
	return config
}

func (w *connectorWrites) rawConfig(id int64) []byte {
	w.t.Helper()
	var raw []byte
	if err := w.pool.QueryRow(context.Background(),
		`SELECT config FROM search_source_connectors WHERE id = $1`, id).Scan(&raw); err != nil {
		w.t.Fatalf("reading connection %d: %v", id, err)
	}
	return raw
}

// opened is a stored server's settings opened as the chat opens them.
func (w *connectorWrites) opened(ownerID string, id int64) (mcpserver.Stored, mcpserver.Target) {
	w.t.Helper()
	var server map[string]json.RawMessage
	if err := json.Unmarshal(w.rawConfig(id), &server); err != nil {
		w.t.Fatal(err)
	}
	stored, err := mcpserver.Parse(server["server_config"])
	if err != nil {
		w.t.Fatalf("connection %d: %v", id, err)
	}
	target, err := mcpserver.Open(testServerSeal, ownerID, stored, nil)
	if err != nil {
		w.t.Fatalf("opening connection %d: %v", id, err)
	}
	return stored, target
}

// estate is everything an account's connections hold, to compare before
// and after another account's attempts.
func (w *connectorWrites) estate(userID string) string {
	w.t.Helper()
	var text string
	if err := w.pool.QueryRow(context.Background(), `
SELECT coalesce(string_agg(id::text || ':' || name || ':' || enabled::text || ':' || config::text || ':' ||
       periodic_indexing_enabled::text || ':' || coalesce(indexing_frequency_minutes::text, '-'), ' | ' ORDER BY id), '')
FROM search_source_connectors WHERE user_id = $1`, userID).Scan(&text); err != nil {
		w.t.Fatal(err)
	}
	return text
}

func idOf(t *testing.T, answered string) int64 {
	t.Helper()
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(answered), &body); err != nil || body.ID == 0 {
		t.Fatalf("no id in %s", answered)
	}
	return body.ID
}

// newServer is the form filled in: a key in the address, a bearer token and
// an extra header, all invented.
const newServer = `{"name": "Search", "server_config": {"url": "https://mcp.example.test/mcp?api_key=fake-query-key-0123456789",
 "transport": "streamable-http", "auth": {"type": "bearer", "token": "fake-header-key-0123456789"},
 "headers": {"X-Team": "fake-team-value-4321"}}}`

// server is a request's server_config with nothing secret, at url.
func server(url string) string {
	return `{"url": "` + url + `", "transport": "streamable-http", "auth": {"type": "none"}}`
}

func TestAddingAnMCPServer(t *testing.T) {
	w := newConnectorWrites(t)
	const alice, bob = testpostgres.Alice, testpostgres.Bob

	status, answered := w.as(alice, http.MethodPost, ServersPath, newServer)
	if status != http.StatusCreated {
		t.Fatalf("answered %d %s", status, answered)
	}
	id := idOf(t, answered)
	for _, shown := range []string{`"auth":{"type":"bearer","token":"••••6789"}`, `"headers":{"X-Team":"••••4321"}`,
		`"url":"https://mcp.example.test/mcp?api_key=••••6789"`, `"transport":"streamable-http"`, `"connector_type":"MCP_CONNECTOR"`} {
		if !strings.Contains(answered, shown) {
			t.Errorf("the answer does not show %s: %s", shown, answered)
		}
	}
	// Stored sealed: no secret in the clear in the row, and the chat opens
	// exactly what was typed.
	if raw := string(w.rawConfig(id)); strings.Contains(raw, "fake-") {
		t.Errorf("a secret is stored in the clear: %s", raw)
	}
	stored, target := w.opened(alice, id)
	if target.Address != "https://mcp.example.test/mcp?api_key=fake-query-key-0123456789" ||
		target.Headers["Authorization"] != "Bearer fake-header-key-0123456789" || target.Headers["X-Team"] != "fake-team-value-4321" ||
		stored.Auth.Type != mcpserver.AuthBearer || w.config(id)["server_origin"] != "member" {
		t.Errorf("stored %+v, opened %+v", stored, target)
	}
	if !slices.Equal(w.tools.accounts, []string{alice}) {
		t.Errorf("the account's MCP counter was moved for %v, want once for her", w.tools.accounts)
	}

	t.Run("the other account cannot read, change or test with it", func(t *testing.T) {
		before := w.estate(alice)
		for _, attempt := range []struct{ method, path, body string }{
			{http.MethodGet, strings.Replace(ServerPath, "{connector_id}", "1", 1), ""},
			{http.MethodPut, strings.Replace(ServerPath, "{connector_id}", "1", 1), `{"name": "Mine now"}`},
			{http.MethodPut, strings.Replace(OnePath, "{connector_id}", "1", 1), `{"name": "Mine now"}`},
			{http.MethodPost, ServerTestPath, `{"url": "https://mcp.example.test/mcp?api_key=••••6789", "transport": "streamable-http",
				"auth": {"type": "bearer", "token": "••••6789"}, "connector_id": 1}`},
		} {
			if status, answered := w.as(bob, attempt.method, attempt.path, attempt.body); status != http.StatusNotFound {
				t.Errorf("%s %s as the other account answered %d %s, want 404", attempt.method, attempt.path, status, answered)
			}
		}
		if after := w.estate(alice); after != before {
			t.Errorf("her connections changed:\n before %s\n after  %s", before, after)
		}
		if w.probe.calls != 0 {
			t.Error("a test was run with another account's stored key")
		}
		// And the same name is free for him: names are per account.
		if status, answered := w.as(bob, http.MethodPost, ServersPath, newServer); status != http.StatusCreated {
			t.Errorf("his own server of the same name answered %d %s", status, answered)
		}
	})

	t.Run("what is refused", func(t *testing.T) {
		before := w.estate(alice)
		with := func(config string) string { return `{"name": "a", "server_config": ` + config + `}` }
		for _, refused := range []struct {
			name, body string
			status     int
			says       string
		}{
			{"a name she already uses", newServer, http.StatusConflict, nameTaken},
			{"plain http", with(server("http://mcp.example.test/mcp")), http.StatusBadRequest, "not https"},
			{"an IP address", with(server("https://93.184.216.34/mcp")), http.StatusBadRequest, "an IP address, not a name"},
			{"a container's name", with(server("https://litellm/mcp")), http.StatusBadRequest, memberNeedsDomain},
			{"a name that resolves inside", with(server("https://inside.example.test/mcp")), http.StatusBadRequest, "not public"},
			{"another port", with(server("https://mcp.example.test:8443/mcp")), http.StatusBadRequest, "port"},
			{"a local command", with(`{"url": "https://mcp.example.test/mcp", "command": "sh"}`), http.StatusUnprocessableEntity, stdioRefused},
			{"the stdio transport", with(`{"url": "https://mcp.example.test/mcp", "transport": "stdio", "auth": {"type": "none"}}`), http.StatusUnprocessableEntity, stdioRefused},
			{"the http alias", with(`{"url": "https://mcp.example.test/mcp", "transport": "http", "auth": {"type": "none"}}`), http.StatusUnprocessableEntity, transportRefused},
			{"no transport", with(`{"url": "https://mcp.example.test/mcp", "auth": {"type": "none"}}`), http.StatusUnprocessableEntity, transportRefused},
			{"no authentication", with(`{"url": "https://mcp.example.test/mcp", "transport": "sse"}`), http.StatusUnprocessableEntity, authRequired},
			{"an unknown authentication", with(`{"url": "https://mcp.example.test/mcp", "transport": "sse", "auth": {"type": "magic"}}`), http.StatusUnprocessableEntity, authKindRefused},
			{"a header key with no header", with(`{"url": "https://mcp.example.test/mcp", "transport": "sse", "auth": {"type": "header", "value": "k"}}`), http.StatusUnprocessableEntity, authHeaderNeeded},
			{"a header name with a space", with(`{"url": "https://mcp.example.test/mcp", "transport": "sse", "auth": {"type": "header", "header_name": "X Key", "value": "k"}}`), http.StatusUnprocessableEntity, headerNameBad},
			{"an empty token", with(`{"url": "https://mcp.example.test/mcp", "transport": "sse", "auth": {"type": "bearer", "token": " "}}`), http.StatusUnprocessableEntity, authTokenRequired},
			{"an extra header repeating the auth one", with(`{"url": "https://mcp.example.test/mcp", "transport": "sse", "auth": {"type": "bearer", "token": "t"}, "headers": {"authorization": "x"}}`), http.StatusUnprocessableEntity, headerIsAuth},
			{"a header given twice", with(`{"url": "https://mcp.example.test/mcp", "transport": "sse", "auth": {"type": "none"}, "headers": {"X-A": "1", "x-a": "2"}}`), http.StatusUnprocessableEntity, headerTwice},
			{"a header on two lines", with(`{"url": "https://mcp.example.test/mcp", "transport": "sse", "auth": {"type": "none"}, "headers": {"X-A": "1\r\nX-B: 2"}}`), http.StatusUnprocessableEntity, headerValueBad},
			{"a setting nobody knows", with(`{"url": "https://mcp.example.test/mcp", "proxy": "x"}`), http.StatusUnprocessableEntity, "Unknown server setting: proxy"},
			{"a masked key with nothing stored", with(`{"url": "https://mcp.example.test/mcp", "transport": "sse", "auth": {"type": "none"}, "headers": {"X-Key": "••••6789"}}`), http.StatusUnprocessableEntity, "Enter the value for the 'X-Key' header again"},
			{"no settings", `{"name": "a"}`, http.StatusUnprocessableEntity, "Field required"},
			{"a blank name", `{"name": "  ", "server_config": ` + server("https://mcp.example.test/mcp") + `}`, http.StatusUnprocessableEntity, "A name is required"},
		} {
			status, answered := w.as(alice, http.MethodPost, ServersPath, refused.body)
			if status != refused.status || !strings.Contains(answered, refused.says) {
				t.Errorf("%s: answered %d %s, want %d saying %q", refused.name, status, answered, refused.status, refused.says)
			}
		}
		// An API key is not a person: refused before anything is checked.
		w.caller = principal.Principal{UserID: alice, Method: principal.MethodPersonalAccessToken}
		if status, answered := w.send(http.MethodPost, ServersPath, strings.Replace(newServer, "Search", "By key", 1)); status != http.StatusForbidden {
			t.Errorf("an API key answered %d %s, want 403", status, answered)
		}
		if after := w.estate(alice); after != before {
			t.Errorf("a refused request changed her connections:\n before %s\n after  %s", before, after)
		}
	})

	t.Run("an OAuth server is saved with its typed client, secret sealed", func(t *testing.T) {
		status, answered := w.as(alice, http.MethodPost, ServersPath, `{"name": "Files", "server_config": {
 "url": "https://files.example.test/mcp", "transport": "sse",
 "auth": {"type": "oauth", "client_id": "typed-client", "client_secret": "fake-client-secret-0001"}}}`)
		if status != http.StatusCreated {
			t.Fatalf("answered %d %s", status, answered)
		}
		want := `"auth":{"type":"oauth","client_id":"typed-client","client_secret":"••••0001","dynamic_registration":false,"authorization_server":null,"authorized":false}`
		if !strings.Contains(answered, want) {
			t.Errorf("answered %s", answered)
		}
		if raw := string(w.rawConfig(idOf(t, answered))); strings.Contains(raw, "fake-client-secret") {
			t.Errorf("the client secret is stored in the clear: %s", raw)
		}
	})

	t.Run("an administrator's server is a site server, held to the site's hosts", func(t *testing.T) {
		testpostgres.Exec(t, w.pool, `UPDATE "user" SET is_superuser = true WHERE id = '`+alice+`'`)
		status, answered := w.as(alice, http.MethodPost, ServersPath,
			`{"name": "Elsewhere", "server_config": `+server("https://mcp.example.test/mcp")+`}`)
		if status != http.StatusBadRequest || !strings.Contains(answered, "not one this server may reach") {
			t.Errorf("a host off the list answered %d %s", status, answered)
		}
		status, answered = w.as(alice, http.MethodPost, ServersPath,
			`{"name": "Site search", "server_config": `+server("https://mcp.site.example/mcp")+`}`)
		if status != http.StatusCreated {
			t.Fatalf("a host on the list answered %d %s", status, answered)
		}
		if origin := w.config(idOf(t, answered))["server_origin"]; origin != "site" {
			t.Errorf("origin %v, want site", origin)
		}
		// The operator's saved list replaces the configured one.
		testpostgres.Exec(t, w.pool, `INSERT INTO site_settings (key, value) VALUES ('mcp_allowed_hosts', '["mcp.example.test"]')
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
		t.Cleanup(func() { testpostgres.Exec(t, w.pool, `DELETE FROM site_settings WHERE key = 'mcp_allowed_hosts'`) })
		if status, answered = w.as(alice, http.MethodPost, ServersPath,
			`{"name": "Elsewhere", "server_config": `+server("https://mcp.example.test/mcp")+`}`); status != http.StatusCreated {
			t.Errorf("a host on the saved list answered %d %s", status, answered)
		}
		_, policy := w.as(alice, http.MethodGet, ServerPolicyPath, "")
		if policy != `{"can_add":true,"allow_list_applies":true,"oauth_available":true}` {
			t.Errorf("an administrator's policy is %s", policy)
		}
		if _, policy = w.as(bob, http.MethodGet, ServerPolicyPath, ""); policy != `{"can_add":true,"allow_list_applies":false,"oauth_available":true}` {
			t.Errorf("a person's policy is %s", policy)
		}
		w.caller = principal.Principal{UserID: bob, Method: principal.MethodPersonalAccessToken}
		if _, policy = w.send(http.MethodGet, ServerPolicyPath, ""); policy != `{"can_add":false,"allow_list_applies":false,"oauth_available":true}` {
			t.Errorf("an API key's policy is %s", policy)
		}
	})
}

func TestChangingAnMCPServer(t *testing.T) {
	w := newConnectorWrites(t)
	const alice = testpostgres.Alice
	// A saved server, with everything an edit must leave alone or clear.
	_, answered := w.as(alice, http.MethodPost, ServersPath, `{"name": "Search", "server_config": {
 "url": "https://mcp.example.test/mcp?api_key=fake-query-key-0123456789&mode=fast", "transport": "streamable-http",
 "auth": {"type": "bearer", "token": "fake-header-key-0123456789"}}}`)
	id := idOf(t, answered)
	if _, other := w.as(alice, http.MethodPost, ServersPath, `{"name": "Other", "server_config": `+server("https://other.example.test/mcp")+`}`); idOf(t, other) == 0 {
		t.Fatal("no second server")
	}
	testpostgres.Exec(t, w.pool, `UPDATE search_source_connectors SET config = config || '{"kept": ["search"], "auth_expired": true,
 "cached_tools": {"tools": [{"name": "search"}]}}'::jsonb WHERE id = `+strconv.FormatInt(id, 10))
	sealedBefore, _ := w.opened(alice, id)
	one := strings.Replace(ServerPath, "{connector_id}", strconv.FormatInt(id, 10), 1)
	generic := strings.Replace(OnePath, "{connector_id}", strconv.FormatInt(id, 10), 1)

	t.Run("a rename alone keeps everything else", func(t *testing.T) {
		status, answered := w.as(alice, http.MethodPut, one, `{"name": "Web search"}`)
		if status != http.StatusOK || !strings.Contains(answered, `"name":"Web search"`) {
			t.Fatalf("answered %d %s", status, answered)
		}
		stored := w.config(id)
		if stored["auth_expired"] != true || stored["cached_tools"] == nil {
			t.Errorf("a rename touched the config: %v", stored)
		}
		if status, answered = w.as(alice, http.MethodPut, one, `{"name": "Other"}`); status != http.StatusConflict {
			t.Errorf("renaming onto her other server answered %d %s, want 409", status, answered)
		}
	})

	t.Run("the form's masked values stand for the stored ones, sealed as they were", func(t *testing.T) {
		w.tools.accounts = nil
		// What the settings page sends: the server as it read it, masks and
		// all, the transport changed and one header added.
		status, answered := w.as(alice, http.MethodPut, generic, `{"name": "Web search", "config": {
 "server_config": {"url": "https://mcp.example.test/mcp?api_key=••••6789&mode=fast", "transport": "sse",
                   "auth": {"type": "bearer", "token": "••••6789"}, "headers": {"X-Team": "reading"}},
 "server_origin": "site", "auth_expired": false}}`)
		if status != http.StatusOK {
			t.Fatalf("answered %d %s", status, answered)
		}
		stored, target := w.opened(alice, id)
		if target.Headers["Authorization"] != "Bearer fake-header-key-0123456789" || target.Headers["X-Team"] != "reading" ||
			target.Address != "https://mcp.example.test/mcp?api_key=fake-query-key-0123456789&mode=fast" || stored.Transport != mcpserver.SSE {
			t.Errorf("opened %+v, stored %+v", target, stored)
		}
		// Kept means copied sealed, not opened and sealed again.
		if stored.Auth.Secret.Sealed != sealedBefore.Auth.Secret.Sealed || stored.URLSealed != sealedBefore.URLSealed {
			t.Error("a kept secret was sealed again")
		}
		config := w.config(id)
		// The request's origin is not what is stored, and what else the
		// config held is kept.
		if config["server_origin"] != "member" || len(config["kept"].([]any)) != 1 {
			t.Errorf("the request changed what only the server decides: %v", config)
		}
		// New settings: the old address's flag and tool list are gone.
		if _, kept := config["auth_expired"]; kept {
			t.Error("auth_expired survived new settings")
		}
		if _, kept := config["cached_tools"]; kept {
			t.Error("the old address's tool list survived new settings")
		}
		if !slices.Equal(w.tools.accounts, []string{alice}) {
			t.Errorf("the counter was moved for %v", w.tools.accounts)
		}
	})

	t.Run("a masked key does not follow the server to another host", func(t *testing.T) {
		before := w.estate(alice)
		for body, says := range map[string]string{
			`{"server_config": {"url": "https://collector.example.test/mcp", "transport": "sse", "auth": {"type": "bearer", "token": "••••6789"}}}`:         keyAgain,
			`{"server_config": {"url": "https://collector.example.test/mcp", "transport": "sse", "auth": {"type": "none"}, "headers": {"X-Team": "••••"}}}`: "header again",
			`{"server_config": {"url": "https://collector.example.test/mcp?api_key=••••6789", "transport": "sse", "auth": {"type": "none"}}}`:               keyAgainInAddress,
			// The same host, but the address edited around its masked key.
			`{"server_config": {"url": "https://mcp.example.test/v2?api_key=••••6789", "transport": "sse", "auth": {"type": "none"}}}`: keyAgainInAddress,
			// The same host, but the token moved into a header.
			`{"server_config": {"url": "https://mcp.example.test/mcp?api_key=••••6789&mode=fast", "transport": "sse", "auth": {"type": "header", "header_name": "X-Key", "value": "••••6789"}}}`: keyAgain,
		} {
			if status, answered := w.as(alice, http.MethodPut, one, body); status != http.StatusUnprocessableEntity || !strings.Contains(answered, says) {
				t.Errorf("%s: answered %d %s", body, status, answered)
			}
		}
		// A config key no read shows is refused by name.
		status, answered := w.as(alice, http.MethodPut, generic, `{"config": {"cached_tools": {"tools": []}}}`)
		if status != http.StatusBadRequest || !strings.Contains(answered, "cached_tools") {
			t.Errorf("answered %d %s", status, answered)
		}
		if after := w.estate(alice); after != before {
			t.Errorf("a refused edit changed the row:\n before %s\n after  %s", before, after)
		}
	})

	t.Run("testing a saved server uses its stored key and reaches only its own host", func(t *testing.T) {
		status, answered := w.as(alice, http.MethodPost, ServerTestPath,
			`{"url": "https://mcp.example.test/mcp?api_key=••••6789&mode=fast", "transport": "sse", "auth": {"type": "bearer", "token": "••••6789"},
			  "connector_id": `+strconv.FormatInt(id, 10)+`}`)
		want := `{"status":"success","message":"Connected successfully. Found 1 tools.","tools":[{"name":"search","description":"Search the web","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}]}`
		if status != http.StatusOK || answered != want {
			t.Fatalf("answered %d %s", status, answered)
		}
		if w.probe.headers["Authorization"] != "Bearer fake-header-key-0123456789" || w.probe.transport != mcpserver.SSE ||
			w.probe.address != "https://mcp.example.test/mcp?api_key=fake-query-key-0123456789&mode=fast" ||
			!slices.Equal(w.probe.hosts, []string{"mcp.example.test"}) {
			t.Errorf("the probe was given %s over %s with headers %v and hosts %v", w.probe.address, w.probe.transport, w.probe.headers, w.probe.hosts)
		}
	})

	t.Run("every failed test is the same answer", func(t *testing.T) {
		generic := `{"status":"error","message":"` + testFailed + `","tools":[]}`
		w.probe.calls = 0
		for name, body := range map[string]string{
			"a masked key and nothing saved": `{"url": "https://mcp.example.test/mcp", "transport": "sse", "auth": {"type": "none"}, "headers": {"X-Key": "••••6789"}}`,
			"a container's name":             server("https://litellm/mcp"),
		} {
			if status, answered := w.as(alice, http.MethodPost, ServerTestPath, body); status != http.StatusOK || answered != generic {
				t.Errorf("%s: answered %d %s", name, status, answered)
			}
		}
		if w.probe.calls != 0 {
			t.Error("a refused test still connected")
		}
		// An OAuth server is tested only with a sign-in.
		signIn := `{"status":"error","message":"` + testNeedsSignIn + `","tools":[]}`
		if status, answered := w.as(alice, http.MethodPost, ServerTestPath,
			`{"url": "https://files.example.test/mcp", "transport": "sse", "auth": {"type": "oauth"}}`); status != http.StatusOK || answered != signIn {
			t.Errorf("an OAuth server before its sign-in: %d %s", status, answered)
		}
		w.probe.fail = true
		if status, answered := w.as(alice, http.MethodPost, ServerTestPath, server("https://mcp.example.test/mcp")); status != http.StatusOK || answered != generic {
			t.Errorf("a server that does not answer: %d %s", status, answered)
		}
		w.caller = principal.Principal{UserID: alice, Method: principal.MethodAgent}
		if status, _ := w.send(http.MethodPost, ServerTestPath, server("https://mcp.example.test/mcp")); status != http.StatusForbidden {
			t.Errorf("an agent's test answered %d, want 403", status)
		}
	})
}

func TestMakingAndChangingAConnection(t *testing.T) {
	w := newConnectorWrites(t)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, w.pool, `
INSERT INTO search_source_connectors (id, user_id, name, connector_type, is_indexable, config) VALUES
 (1, '`+alice+`', 'Drive', 'GOOGLE_DRIVE_CONNECTOR', true,
     '{"refresh_token": "fake-alice-grant", "selected_folders": [{"id": "old"}]}'),
 (2, '`+alice+`', 'Mail', 'EMAIL_IMAP_CONNECTOR', false,
     '{"accounts": [{"_encrypted": true, "id": "m1", "email": "a@example.test", "password": "fake-ciphertext"}]}'),
 (3, '`+alice+`', 'Cloud', 'NEXTCLOUD_CONNECTOR', false, '{"app_password": "fake-alice-password"}');
SELECT setval(pg_get_serial_sequence('search_source_connectors', 'id'), 10);`)
	path := func(id string) string { return strings.Replace(OnePath, "{connector_id}", id, 1) }

	t.Run("a web-pages connection is made once", func(t *testing.T) {
		body := `{"name": "Web Pages", "connector_type": "WEBCRAWLER_CONNECTOR", "config": {}, "is_indexable": false,
 "is_active": true, "last_indexed_at": null, "periodic_indexing_enabled": false,
 "indexing_frequency_minutes": null, "next_scheduled_at": null, "enable_vision_llm": false}`
		status, answered := w.as(alice, http.MethodPost, ListPath, body)
		if status != http.StatusOK || !strings.Contains(answered, `"is_indexable":true`) {
			t.Fatalf("answered %d %s", status, answered)
		}
		first := idOf(t, answered)
		if _, again := w.as(alice, http.MethodPost, ListPath, body); idOf(t, again) != first {
			t.Errorf("a second click made a second connection: %s", again)
		}
		if _, his := w.as(bob, http.MethodPost, ListPath, body); idOf(t, his) == first {
			t.Error("the other account was handed her connection")
		}
	})

	t.Run("what the create refuses", func(t *testing.T) {
		for _, refused := range []struct {
			body   string
			status int
			says   string
		}{
			{`{"name": "d", "connector_type": "GOOGLE_DRIVE_CONNECTOR", "config": {}}`, http.StatusBadRequest, "Connect Google Drive"},
			{`{"name": "n", "connector_type": "NEXTCLOUD_CONNECTOR", "config": {"app_password": "x"}}`, http.StatusBadRequest, "Connect Nextcloud"},
			{`{"name": "m", "connector_type": "EMAIL_IMAP_CONNECTOR", "config": {}}`, http.StatusBadRequest, "Add mailboxes"},
			{`{"name": "s", "connector_type": "MCP_CONNECTOR", "config": {}}`, http.StatusBadRequest, notAServerHere},
			{`{"name": "s", "connector_type": "WEBCRAWLER_CONNECTOR", "config": {"server_config": {}}}`, http.StatusBadRequest, notAServerHere},
			{`{"name": "w", "connector_type": "WEBCRAWLER_CONNECTOR", "config": {"FIRECRAWL_API_KEY": "k"}}`, http.StatusBadRequest, "FIRECRAWL_API_KEY"},
			{`{"name": "w", "connector_type": "SLACK_CONNECTOR", "config": {}}`, http.StatusUnprocessableEntity, "not a kind of connection"},
			{`{"name": "w", "connector_type": "WEBCRAWLER_CONNECTOR", "config": {}, "periodic_indexing_enabled": true}`, http.StatusUnprocessableEntity, frequencyRequired},
		} {
			if status, answered := w.as(alice, http.MethodPost, ListPath, refused.body); status != refused.status || !strings.Contains(answered, refused.says) {
				t.Errorf("%s: answered %d %s", refused.body, status, answered)
			}
		}
	})

	t.Run("the other account changes nothing of hers", func(t *testing.T) {
		before := w.estate(alice)
		for _, id := range []string{"1", "2", "3"} {
			if status, answered := w.as(bob, http.MethodPut, path(id), `{"name": "His", "config": {}}`); status != http.StatusNotFound {
				t.Errorf("connection %s as the other account answered %d %s", id, status, answered)
			}
		}
		if after := w.estate(alice); after != before {
			t.Errorf("her connections changed:\n before %s\n after  %s", before, after)
		}
	})

	t.Run("settings merge over the stored config and never carry a sign-in", func(t *testing.T) {
		status, answered := w.as(alice, http.MethodPut, path("1"),
			`{"name": "My Drive", "enable_vision_llm": true, "config": {"selected_folders": [{"id": "new"}], "auth_expired": false}}`)
		if status != http.StatusOK || !strings.Contains(answered, `"name":"My Drive"`) || !strings.Contains(answered, `"enable_vision_llm":true`) {
			t.Fatalf("answered %d %s", status, answered)
		}
		stored := w.config(1)
		if stored["refresh_token"] != "fake-alice-grant" {
			t.Errorf("the grant did not survive: %v", stored)
		}
		if folders := stored["selected_folders"].([]any); folders[0].(map[string]any)["id"] != "new" {
			t.Errorf("the picks were not replaced: %v", stored)
		}
		if _, written := stored["auth_expired"]; written {
			t.Error("a request wrote the reconnect flag")
		}
		before := w.estate(alice)
		for _, body := range []string{
			`{"config": {"refresh_token": "fake-someone-elses-grant"}}`,
			`{"config": {"token_uri": "https://collector.example.test"}}`,
			`{"connector_type": "WEBCRAWLER_CONNECTOR"}`,
		} {
			if status, answered := w.as(alice, http.MethodPut, path("1"), body); status != http.StatusBadRequest {
				t.Errorf("%s answered %d %s, want 400", body, status, answered)
			}
		}
		if status, _ := w.as(alice, http.MethodPut, path("3"), `{"config": {"app_password": "fake-pasted"}}`); status != http.StatusBadRequest {
			t.Errorf("a pasted Nextcloud password answered %d", status)
		}
		// The page sends a mailbox connection's reduced accounts back; the
		// stored ones, ciphertext and all, stay.
		if status, answered := w.as(alice, http.MethodPut, path("2"),
			`{"name": "Mail", "config": {"accounts": [{"id": "m1", "email": "a@example.test"}]}}`); status != http.StatusOK {
			t.Errorf("answered %d %s", status, answered)
		}
		if after := w.estate(alice); after != before {
			t.Errorf("a refused or empty edit changed a row:\n before %s\n after  %s", before, after)
		}
	})

	t.Run("the schedule is the server's", func(t *testing.T) {
		planned := func(id int64) (enabled bool, minutes *int64, next *time.Time) {
			if err := w.pool.QueryRow(context.Background(), `SELECT periodic_indexing_enabled, indexing_frequency_minutes, next_scheduled_at
				FROM search_source_connectors WHERE id = $1`, id).Scan(&enabled, &minutes, &next); err != nil {
				t.Fatal(err)
			}
			return
		}
		// The client's own next run is ignored; it is now plus the pace.
		status, answered := w.as(alice, http.MethodPut, path("1"),
			`{"periodic_indexing_enabled": true, "indexing_frequency_minutes": 60, "next_scheduled_at": "2001-01-01T00:00:00Z"}`)
		if status != http.StatusOK {
			t.Fatalf("answered %d %s", status, answered)
		}
		if enabled, minutes, next := planned(1); !enabled || *minutes != 60 || !next.Equal(fixedNow.Add(time.Hour)) {
			t.Errorf("schedule %v %v %v", enabled, minutes, next)
		}
		// A rename does not restart the wait.
		testpostgres.Exec(t, w.pool, `UPDATE search_source_connectors SET next_scheduled_at = '2026-11-12T09:10:00Z' WHERE id = 1`)
		w.as(alice, http.MethodPut, path("1"), `{"name": "Drive again"}`)
		if _, _, next := planned(1); !next.Equal(fixedNow.Add(10 * time.Minute)) {
			t.Errorf("a rename moved the next run to %v", next)
		}
		// Off: nothing planned, the pace kept.
		w.as(alice, http.MethodPut, path("1"), `{"periodic_indexing_enabled": false}`)
		if enabled, minutes, next := planned(1); enabled || next != nil || *minutes != 60 {
			t.Errorf("switched off: %v %v %v", enabled, minutes, next)
		}
		for body, says := range map[string]string{
			`{"periodic_indexing_enabled": true, "indexing_frequency_minutes": 0}`:    frequencyRange,
			`{"periodic_indexing_enabled": true, "indexing_frequency_minutes": null}`: frequencyRequired,
		} {
			if status, answered := w.as(alice, http.MethodPut, path("1"), body); status != http.StatusUnprocessableEntity || !strings.Contains(answered, says) {
				t.Errorf("%s answered %d %s", body, status, answered)
			}
		}
		// A mailbox connection is not brought in by an indexing run.
		if status, answered := w.as(alice, http.MethodPut, path("2"),
			`{"periodic_indexing_enabled": true, "indexing_frequency_minutes": 60}`); status != http.StatusUnprocessableEntity || !strings.Contains(answered, notIndexable) {
			t.Errorf("answered %d %s", status, answered)
		}
	})
}
