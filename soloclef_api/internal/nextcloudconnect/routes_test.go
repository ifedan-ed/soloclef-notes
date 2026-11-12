package nextcloudconnect

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
)

// databaseVariable names a throwaway Postgres for this package's tests.
// **Every account in it is deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_NEXTCLOUDCONNECT_DATABASE"

const secretKey = "a secret key made up for this test"

// cloud is a Nextcloud that speaks just enough: Login Flow v2, the account
// lookup, one PROPFIND and the app-password delete.
type cloud struct {
	*httptest.Server
	mu sync.Mutex
	// granted is the app password the next poll hands over, once.
	granted string
	// issued are the app passwords that still sign in.
	issued map[string]bool
	// revoked are the ones deleted, in order.
	revoked []string
	// filesStatus is what the PROPFIND answers.
	filesStatus int
	// pollEndpoint and loginPage override what the start reply names.
	pollEndpoint, loginPage string
	polls                   int
}

func newCloud(t *testing.T) *cloud {
	t.Helper()
	c := &cloud{issued: map[string]bool{}, filesStatus: http.StatusMultiStatus}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		_, password, _ := r.BasicAuth()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/index.php/login/v2":
			endpoint, page := c.URL+"/login/v2/poll", c.URL+"/login/v2/flow/abc"
			if c.pollEndpoint != "" {
				endpoint = c.pollEndpoint
			}
			if c.loginPage != "" {
				page = c.loginPage
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"login": page,
				"poll": map[string]string{"endpoint": endpoint, "token": "fake-poll-token"}})
		case r.Method == http.MethodPost && r.URL.Path == "/login/v2/poll":
			c.polls++
			if r.FormValue("token") != "fake-poll-token" || c.granted == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			c.issued[c.granted] = true
			_ = json.NewEncoder(w).Encode(map[string]string{"loginName": "ada@example.test", "appPassword": c.granted})
			c.granted = ""
		case r.Method == http.MethodGet && r.URL.Path == "/ocs/v2.php/cloud/user":
			if !c.issued[password] || r.Header.Get("OCS-APIRequest") != "true" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"ocs": {"data": {"id": "ada", "display-name": "Ada L."}}}`)
		case r.Method == "PROPFIND" && r.URL.Path == "/remote.php/dav/files/ada/":
			if !c.issued[password] {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(c.filesStatus)
		case r.Method == http.MethodDelete && r.URL.Path == "/ocs/v2.php/core/apppassword":
			if !c.issued[password] {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			delete(c.issued, password)
			c.revoked = append(c.revoked, password)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(c.Close)
	return c
}

// grant makes the next poll hand over this app password.
func (c *cloud) grant(password string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.granted = password
}

// memory is KeyValue in a map, with the lifetime recorded and not applied.
type memory struct {
	mu     sync.Mutex
	values map[string]string
}

func (m *memory) Set(_ context.Context, key, value string, lifetime time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lifetime != flowLifetime {
		panic("a pending sign-in was kept for " + lifetime.String())
	}
	m.values[key] = value
	return nil
}

func (m *memory) Get(_ context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, found := m.values[key]
	return value, found, nil
}

func (m *memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	return nil
}

type site struct {
	t       *testing.T
	pool    *pgxpool.Pool
	cloud   *cloud
	kept    *memory
	creds   nextcloud.Credentials
	handler http.Handler
	caller  principal.Principal
}

// newSite mounts the routes over the real statements and a fake Nextcloud.
// configured false is a deployment that names no server.
func newSite(t *testing.T, configured bool) *site {
	t.Helper()
	s := &site{t: t, pool: testpostgres.Open(t, databaseVariable), cloud: newCloud(t), kept: &memory{values: map[string]string{}}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes := Routes{Rows: Store{Pool: s.pool}, Now: time.Now, Logger: logger,
		Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}}
	if configured {
		credentials, err := nextcloud.NewCredentials(secretKey, s.cloud.URL+"/")
		if err != nil {
			t.Fatal(err)
		}
		s.creds = credentials
		api, err := NewLoginAPI(credentials.ServerURL, http.DefaultTransport, "Notes under test")
		if err != nil {
			t.Fatal(err)
		}
		routes.Credentials, routes.API = &credentials, api
		routes.Pending = Pending{Store: s.kept, SecretKey: secretKey, Now: time.Now}
	}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	s.handler = requestcontext.Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.Adapter().ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), s.caller)))
	}))
	return s
}

func (s *site) as(userID, method, path string) (int, string) {
	s.t.Helper()
	s.caller = principal.Principal{UserID: userID, Method: principal.MethodSession}
	return s.send(method, path)
}

func (s *site) send(method, path string) (int, string) {
	s.t.Helper()
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	answered := recorder.Body.String()
	// No answer of any route holds a poll token, an app password or a
	// sealed one. Every invented secret here begins "fake-", and a sealed
	// token begins "gAAAA".
	if strings.Contains(answered, "fake-") || strings.Contains(answered, "gAAAA") {
		s.t.Fatalf("%s %s answered a credential: %s", method, path, answered)
	}
	return recorder.Code, answered
}

func (s *site) start(userID string) string {
	s.t.Helper()
	status, answered := s.as(userID, http.MethodPost, LoginPath)
	var body struct {
		FlowID    string `json:"flow_id"`
		LoginURL  string `json:"login_url"`
		ExpiresIn int    `json:"expires_in"`
	}
	if status != http.StatusOK || json.Unmarshal([]byte(answered), &body) != nil || body.FlowID == "" {
		s.t.Fatalf("starting a sign-in answered %d %s", status, answered)
	}
	if body.LoginURL != s.cloud.URL+"/login/v2/flow/abc" || body.ExpiresIn != 1200 {
		s.t.Errorf("the start answered %+v", body)
	}
	return body.FlowID
}

func pollPath(flowID string) string { return strings.Replace(PollPath, "{flow_id}", flowID, 1) }

// rows is the account's Nextcloud connections as stored.
func (s *site) rows(userID string) (ids []int64, configs []string, enabled []bool) {
	s.t.Helper()
	found, err := s.pool.Query(context.Background(), `SELECT id, config::text, enabled FROM search_source_connectors
		WHERE user_id = $1 AND connector_type = 'NEXTCLOUD_CONNECTOR' ORDER BY id`, userID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer found.Close()
	for found.Next() {
		var id int64
		var config string
		var on bool
		if err := found.Scan(&id, &config, &on); err != nil {
			s.t.Fatal(err)
		}
		ids, configs, enabled = append(ids, id), append(configs, config), append(enabled, on)
	}
	return
}

func TestConnectingAndDisconnectingNextcloud(t *testing.T) {
	s := newSite(t, true)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	host := strings.TrimPrefix(s.cloud.URL, "http://")

	status, answered := s.as(alice, http.MethodGet, ConnectionPath)
	if want := `{"configured":true,"server":"` + host + `","connector_id":null,"account":null,"needs_reconnect":false}`; status != http.StatusOK || answered != want {
		t.Fatalf("before connecting: %d %s", status, answered)
	}

	flow := s.start(alice)
	// The token is sealed where it is kept.
	for _, record := range s.kept.values {
		if strings.Contains(record, "fake-poll-token") {
			t.Errorf("the poll token is kept in the clear: %s", record)
		}
	}

	t.Run("a sign-in is its starter's alone", func(t *testing.T) {
		s.cloud.grant("fake-app-password-one")
		polls := s.cloud.polls
		if status, answered := s.as(bob, http.MethodPost, pollPath(flow)); status != http.StatusNotFound || !strings.Contains(answered, flowGoneCode) {
			t.Errorf("the other account's poll answered %d %s", status, answered)
		}
		if status, _ := s.as(bob, http.MethodDelete, strings.Replace(CancelPath, "{flow_id}", flow, 1)); status != http.StatusNoContent {
			t.Errorf("the other account's cancel answered %d", status)
		}
		if s.cloud.polls != polls || len(s.kept.values) != 1 {
			t.Error("the other account's requests reached Nextcloud or removed the sign-in")
		}
		if ids, _, _ := s.rows(bob); len(ids) != 0 {
			t.Error("the other account was given a connection")
		}
		s.cloud.grant("")
	})

	t.Run("pending until granted, then saved sealed", func(t *testing.T) {
		if status, answered := s.as(alice, http.MethodPost, pollPath(flow)); status != http.StatusOK || answered != `{"status":"pending","connection":null}` {
			t.Fatalf("before the grant: %d %s", status, answered)
		}
		s.cloud.grant("fake-app-password-one")
		status, answered := s.as(alice, http.MethodPost, pollPath(flow))
		if status != http.StatusOK || !strings.Contains(answered, `"status":"connected"`) ||
			!strings.Contains(answered, `"account":{"user_id":"ada","display_name":"Ada L."}`) || !strings.Contains(answered, `"needs_reconnect":false`) {
			t.Fatalf("after the grant: %d %s", status, answered)
		}
		ids, configs, _ := s.rows(alice)
		if len(ids) != 1 || strings.Contains(configs[0], "fake-app-password-one") {
			t.Fatalf("stored %v %v", ids, configs)
		}
		// What every reader of a Nextcloud row reads, the import included.
		account, err := s.creds.Account([]byte(configs[0]))
		if err != nil || account.AppPassword != "fake-app-password-one" || account.Username != "ada" || account.LoginName != "ada@example.test" {
			t.Errorf("the stored connection reads as %+v, %v", account, err)
		}
		// The flow is spent.
		if status, _ := s.as(alice, http.MethodPost, pollPath(flow)); status != http.StatusNotFound {
			t.Errorf("a second poll of a finished sign-in answered %d", status)
		}
	})

	t.Run("connecting again replaces the login in the same row and ends the old one", func(t *testing.T) {
		before, _, _ := s.rows(alice)
		testpostgres.Exec(t, s.pool, `UPDATE search_source_connectors SET enabled = false, name = 'Work cloud' WHERE id = `+jsonNumber(before[0]))
		again := s.start(alice)
		s.cloud.grant("fake-app-password-two")
		if status, answered := s.as(alice, http.MethodPost, pollPath(again)); status != http.StatusOK {
			t.Fatalf("answered %d %s", status, answered)
		}
		ids, configs, enabled := s.rows(alice)
		if len(ids) != 1 || ids[0] != before[0] || !enabled[0] {
			t.Errorf("rows %v enabled %v, want the one row %v switched on", ids, enabled, before)
		}
		if account, _ := s.creds.Account([]byte(configs[0])); account.AppPassword != "fake-app-password-two" {
			t.Error("the new password was not stored")
		}
		if len(s.cloud.revoked) != 1 || s.cloud.revoked[0] != "fake-app-password-one" {
			t.Errorf("revoked at Nextcloud: %v", s.cloud.revoked)
		}
	})

	t.Run("only the owner disconnects; the imported files stay", func(t *testing.T) {
		ids, _, _ := s.rows(alice)
		id := jsonNumber(ids[0])
		testpostgres.Exec(t, s.pool, `INSERT INTO documents (id, user_id, title, document_type, content, content_hash, connector_id)
			VALUES (70, '`+alice+`', 'From the cloud', 'FILE', 'text', 'hash-70', `+id+`)`)
		path := strings.Replace(DisconnectPath, "{connector_id}", id, 1)
		if status, answered := s.as(bob, http.MethodDelete, path); status != http.StatusNotFound {
			t.Errorf("the other account's disconnect answered %d %s", status, answered)
		}
		s.caller = principal.Principal{UserID: alice, Method: principal.MethodPersonalAccessToken}
		if status, _ := s.send(http.MethodDelete, path); status != http.StatusForbidden {
			t.Errorf("an API key's disconnect answered %d", status)
		}
		if ids, _, _ := s.rows(alice); len(ids) != 1 {
			t.Fatal("her connection was removed by someone else")
		}
		if status, answered := s.as(alice, http.MethodDelete, path); status != http.StatusOK || answered != `{"revoked":true}` {
			t.Errorf("her disconnect answered %d %s", status, answered)
		}
		if ids, _, _ := s.rows(alice); len(ids) != 0 {
			t.Error("the connection is still there")
		}
		if got := s.cloud.revoked; len(got) != 2 || got[1] != "fake-app-password-two" {
			t.Errorf("revoked at Nextcloud: %v", got)
		}
		var kept int
		if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM documents WHERE id = 70 AND connector_id IS NULL`).Scan(&kept); err != nil || kept != 1 {
			t.Errorf("the imported document: %d left unlinked, %v", kept, err)
		}
		if status, _ := s.as(alice, http.MethodDelete, path); status != http.StatusNotFound {
			t.Errorf("disconnecting twice answered %d", status)
		}
	})

	t.Run("a login that cannot list files is ended, not saved", func(t *testing.T) {
		s.cloud.filesStatus = http.StatusForbidden
		defer func() { s.cloud.filesStatus = http.StatusMultiStatus }()
		flow := s.start(alice)
		s.cloud.grant("fake-app-password-three")
		status, answered := s.as(alice, http.MethodPost, pollPath(flow))
		if status != http.StatusBadGateway || !strings.Contains(answered, `"code":"`+unusableCode+`"`) {
			t.Errorf("answered %d %s", status, answered)
		}
		if ids, _, _ := s.rows(alice); len(ids) != 0 {
			t.Error("an unusable login was saved")
		}
		if got := s.cloud.revoked; got[len(got)-1] != "fake-app-password-three" {
			t.Errorf("the unusable password was left alive: %v", got)
		}
	})

	t.Run("what Nextcloud's start reply may not do", func(t *testing.T) {
		for _, bad := range []struct{ endpoint, page string }{
			{"https://collector.example.test/poll", ""},
			{"", "javascript:alert(1)"},
		} {
			s.cloud.pollEndpoint, s.cloud.loginPage = bad.endpoint, bad.page
			if status, answered := s.as(alice, http.MethodPost, LoginPath); status != http.StatusBadGateway {
				t.Errorf("%+v: answered %d %s", bad, status, answered)
			}
		}
		s.cloud.pollEndpoint, s.cloud.loginPage = "", ""
	})
}

func TestADeploymentWithoutNextcloud(t *testing.T) {
	s := newSite(t, false)
	const alice = testpostgres.Alice
	// A row left from when it had one.
	testpostgres.Exec(t, s.pool, `INSERT INTO search_source_connectors (id, user_id, name, connector_type, config)
		VALUES (1, '`+alice+`', 'Nextcloud', 'NEXTCLOUD_CONNECTOR', '{"username": "ada", "app_password": "fake-old", "server_url": "https://gone.example.test"}')`)
	status, answered := s.as(alice, http.MethodGet, ConnectionPath)
	if want := `{"configured":false,"server":null,"connector_id":1,"account":{"user_id":"ada","display_name":"ada"},"needs_reconnect":true}`; status != http.StatusOK || answered != want {
		t.Errorf("status: %d %s", status, answered)
	}
	for _, path := range []string{LoginPath, pollPath("anything")} {
		if status, answered := s.as(alice, http.MethodPost, path); status != http.StatusServiceUnavailable || !strings.Contains(answered, notConfiguredCode) {
			t.Errorf("%s answered %d %s", path, status, answered)
		}
	}
	// It can still be removed; there is no server to end the password at.
	if status, answered := s.as(alice, http.MethodDelete, strings.Replace(DisconnectPath, "{connector_id}", "1", 1)); status != http.StatusOK || answered != `{"revoked":false}` {
		t.Errorf("disconnect answered %d %s", status, answered)
	}
}

func jsonNumber(id int64) string {
	text, _ := json.Marshal(id)
	return string(text)
}
