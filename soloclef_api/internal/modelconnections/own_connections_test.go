package modelconnections

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// The routes for a person's own OpenAI-compatible connection, against stub
// OpenAI-compatible servers on loopback. Loopback is inside this server's
// network, so a stub is reachable only because these tests put 127.0.0.1 on
// the operator's allow-list; the guard's refusals are tested with no list.

// guardedEndpoints is the endpoint client with the operator's list given.
func guardedEndpoints(allowed ...string) EndpointClient {
	return EndpointClient{Timeout: 5 * time.Second, Egress: ownmodels.Egress{
		Resolve: func(_ context.Context, host string) ([]netip.Addr, error) {
			// Every name a test uses is made to resolve inside.
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		AllowedHosts:   func(context.Context) ([]string, error) { return allowed, nil },
		ConnectTimeout: 2 * time.Second,
	}}
}

// loopbackEndpoints reaches the stub servers.
func loopbackEndpoints() EndpointClient { return guardedEndpoints("127.0.0.1") }

// stub is an OpenAI-compatible server that records what it was sent.
type stub struct {
	*httptest.Server
	mu       sync.Mutex
	requests []seenRequest
	// listStatus is what GET /models answers; 200 lists the models.
	listStatus int
	models     []string
}

type seenRequest struct {
	method, path, auth, org, model string
}

func newStub(t *testing.T, models ...string) *stub {
	t.Helper()
	s := &stub{listStatus: http.StatusOK, models: models}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.requests = append(s.requests, seenRequest{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Org"), body.Model})
		status, models := s.listStatus, s.models
		s.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fake-good-key" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"message":"bad key fake-good-key-echo"}}`)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			if status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
			data := make([]map[string]string, len(models))
			for i, id := range models {
				data[i] = map[string]string{"id": id, "object": "model", "owned_by": "stub"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
		case "/v1/chat/completions":
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *stub) seen() []seenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]seenRequest(nil), s.requests...)
}

// ownRoutes is the list and every write route over a real database, as one
// caller, with the endpoint client given.
func ownRoutes(t *testing.T, pool *pgxpool.Pool, endpoints Endpoints, caller principal.Principal) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	store := Store{Pool: pool}
	routes := WriteRoutes{Connections: store, Keys: seal(t), Prober: &recorder{}, Catalogue: testShelf(), Endpoints: endpoints, Errors: envelope, Logger: logger}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	ConnectionsRoute{Connections: store, Errors: envelope, Logger: logger}.Register(api)
	routes.Register(api)
	bodies := requestbody.Middleware{Covers: func(*http.Request) bool { return true }}
	return bodies.Wrap(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), caller)))
	}))
}

// codeOf is the error envelope's code.
func codeOf(body string) string {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &envelope)
	return envelope.Error.Code
}

// An account that administers nothing adds, lists, tests and removes its
// own OpenAI-compatible connection, and is still refused every other
// provider.
func TestAnyAccountManagesItsOwnOpenAICompatibleConnection(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const bob = testpostgres.Bob
	endpoint := newStub(t, "stub-large", "stub-small", "stub-small")
	his := ownRoutes(t, pool, loopbackEndpoints(), sessionOf(bob))
	base := endpoint.URL + "/v1"

	// The endpoint's own list, read with the key and the headers he typed.
	status, body := send(t, his, http.MethodPost, DiscoverPreviewPath,
		`{"provider":"openai_compatible","base_url":"`+base+`","api_key":"fake-good-key","headers":{"X-Org":"org-bob"}}`)
	if status != http.StatusOK || !strings.Contains(body, `"model_id":"stub-large"`) || strings.Count(body, `"model_id":"stub-small"`) != 1 ||
		!strings.Contains(body, `"source":"endpoint"`) {
		t.Fatalf("discover-preview = %d %s", status, body)
	}
	if seen := endpoint.seen(); len(seen) != 1 || seen[0] != (seenRequest{"GET", "/v1/models", "Bearer fake-good-key", "org-bob", ""}) {
		t.Fatalf("the endpoint saw %+v", seen)
	}

	// Tested at its own address before it is made.
	status, body = send(t, his, http.MethodPost, TestPreviewPath,
		`{"provider":"openai_compatible","base_url":"`+base+`","api_key":"fake-good-key","headers":{"X-Org":"org-bob"},"model_id":"stub-large"}`)
	if status != http.StatusOK || !strings.Contains(body, `"status":"OK"`) {
		t.Fatalf("test-preview = %d %s", status, body)
	}
	if last := endpoint.seen()[1]; last != (seenRequest{"POST", "/v1/chat/completions", "Bearer fake-good-key", "org-bob", "stub-large"}) {
		t.Fatalf("the test sent %+v", last)
	}

	status, body = send(t, his, http.MethodPost, ConnectionsPath,
		`{"provider":"openai_compatible","base_url":"`+base+`","api_key":"fake-good-key","headers":{"x-org":"org-bob"},"models":[{"model_id":"stub-large","enabled":true}]}`)
	if status != http.StatusOK || !strings.Contains(body, `"header_names":["X-Org"]`) || strings.Contains(body, "org-bob") ||
		strings.Contains(body, "fake-") || strings.Contains(body, "headers_sealed") {
		t.Fatalf("create = %d %s", status, body)
	}
	var made struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &made)
	id := fmt.Sprint(made.ID)

	// The headers are sealed at rest like the key: the value is in no
	// column as typed.
	var extra string
	if err := pool.QueryRow(context.Background(), `SELECT extra::text FROM connections WHERE id = $1`, made.ID).Scan(&extra); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(extra, "org-bob") || !strings.Contains(extra, "headers_sealed") {
		t.Fatalf("extra as stored: %s", extra)
	}

	// The stored connection is verified and refreshed with its stored key
	// and headers, never sent back to the browser.
	if status, body = send(t, his, http.MethodPost, "/api/v1/model-connections/"+id+"/verify", ""); status != http.StatusOK || !strings.Contains(body, `"status":"OK"`) {
		t.Fatalf("verify = %d %s", status, body)
	}
	if status, body = send(t, his, http.MethodPost, "/api/v1/model-connections/"+id+"/discover", ""); status != http.StatusOK ||
		!strings.Contains(body, `"model_id":"stub-small","display_name":null,"source":"DISCOVERED"`) {
		t.Fatalf("discover = %d %s", status, body)
	}
	for _, seen := range endpoint.seen()[2:] {
		if seen.auth != "Bearer fake-good-key" || seen.org != "org-bob" {
			t.Fatalf("a stored call lost its key or header: %+v", seen)
		}
	}

	// A change that leaves the headers out keeps them; null removes them.
	if status, body = send(t, his, http.MethodPut, "/api/v1/model-connections/"+id, `{"enabled":true}`); status != http.StatusOK || !strings.Contains(body, `"header_names":["X-Org"]`) {
		t.Fatalf("update keeping headers = %d %s", status, body)
	}
	if status, body = send(t, his, http.MethodPut, "/api/v1/model-connections/"+id, `{"headers":null}`); status != http.StatusOK || !strings.Contains(body, `"header_names":[]`) {
		t.Fatalf("update removing headers = %d %s", status, body)
	}

	// Every other provider is still an administrator's.
	for _, call := range []struct{ method, path, body string }{
		{http.MethodPost, ConnectionsPath, `{"provider":"openai","api_key":"fake-k"}`},
		{http.MethodPost, TestPreviewPath, `{"provider":"anthropic","model_id":"m"}`},
		{http.MethodPost, DiscoverPreviewPath, `{"provider":"openai"}`},
		// Nor may his own connection be moved to one.
		{http.MethodPut, "/api/v1/model-connections/" + id, `{"provider":"openai"}`},
	} {
		if status, body := send(t, his, call.method, call.path, call.body); status != http.StatusForbidden || !strings.Contains(body, "Anyone can add an OpenAI-compatible connection") {
			t.Errorf("%s %s = %d %s, want 403", call.method, call.path, status, body)
		}
	}

	if status, body = send(t, his, http.MethodDelete, "/api/v1/model-connections/"+id, ""); status != http.StatusOK {
		t.Fatalf("delete = %d %s", status, body)
	}
}

// What an OpenAI-compatible connection must hold, refused before anything
// is stored or called.
func TestWhatAnOpenAICompatibleConnectionMustHold(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	administrators(t, pool)
	endpoint := newStub(t)
	hers := ownRoutes(t, pool, loopbackEndpoints(), sessionOf(testpostgres.Alice))
	for body, want := range map[string]string{
		`{"provider":"openai_compatible","api_key":"fake-k"}`:                                                                   "needs the endpoint's base URL",
		`{"provider":"openai_compatible","base_url":"` + endpoint.URL + `"}`:                                                    "needs the endpoint's API key",
		`{"provider":"openai_compatible","base_url":"https://u:fake-p@models.example","api_key":"fake-k"}`:                      "base_url must be an http or https address",
		`{"provider":"openai_compatible","base_url":"` + endpoint.URL + `","api_key":"fake-k","headers":{"Authorization":"x"}}`: "set by Soloclef",
		`{"provider":"openai_compatible","base_url":"` + endpoint.URL + `","api_key":"fake-k","extra":{"header_names":["X"]}}`:  "send the headers as headers",
		`{"provider":"openai","api_key":"fake-k","headers":{"X-Org":"o"}}`:                                                      "only to an OpenAI-compatible endpoint",
	} {
		if status, answer := send(t, hers, http.MethodPost, ConnectionsPath, body); status != http.StatusUnprocessableEntity || !strings.Contains(answer, want) {
			t.Errorf("create %s = %d %s, want a 422 with %q", body, status, answer, want)
		}
	}
	var stored int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM connections`).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("refused creates stored %d rows, %v", stored, err)
	}
	if len(endpoint.seen()) != 0 {
		t.Errorf("a refused create called the endpoint: %+v", endpoint.seen())
	}
}

// Discovery says why there is no list, in the words the screen shows, and
// the guard refuses an address inside this server's network.
func TestDiscoveryOfAnEndpointSaysWhyThereIsNoList(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	endpoint := newStub(t, "m")
	preview := func(handler http.Handler, key string) (int, string) {
		return send(t, handler, http.MethodPost, DiscoverPreviewPath,
			`{"provider":"openai_compatible","base_url":"`+endpoint.URL+`/v1","api_key":"`+key+`"}`)
	}
	reach := ownRoutes(t, pool, loopbackEndpoints(), sessionOf(testpostgres.Alice))

	status, body := preview(reach, "fake-wrong-key")
	if status != http.StatusBadRequest || codeOf(body) != codeKeyRefused || strings.Contains(body, "fake-") {
		t.Errorf("a refused key = %d %s", status, body)
	}
	endpoint.mu.Lock()
	endpoint.listStatus = http.StatusNotFound
	endpoint.mu.Unlock()
	status, body = preview(reach, "fake-good-key")
	if status != http.StatusConflict || codeOf(body) != codeListUnsupported || !strings.Contains(body, "GET /models answered 404") || !strings.Contains(body, "Type the model name") {
		t.Errorf("no list = %d %s", status, body)
	}

	// With no host allowed inside, loopback is refused, as is a name the
	// resolver says is inside: before any request reaches the stub.
	called := len(endpoint.seen())
	guarded := ownRoutes(t, pool, guardedEndpoints(), sessionOf(testpostgres.Alice))
	status, body = preview(guarded, "fake-good-key")
	if status != http.StatusBadRequest || codeOf(body) != codeAddressRefused || !strings.Contains(body, "inside this server's network") {
		t.Errorf("an inside address = %d %s", status, body)
	}
	status, body = send(t, guarded, http.MethodPost, TestPreviewPath,
		`{"provider":"openai_compatible","base_url":"http://models.example.test:`+strings.Split(endpoint.URL, ":")[2]+`/v1","api_key":"fake-good-key","model_id":"m"}`)
	if status != http.StatusOK || !strings.Contains(body, `"status":"UNREACHABLE"`) || !strings.Contains(body, "models.example.test points inside") {
		t.Errorf("a test of an inside name = %d %s", status, body)
	}
	if len(endpoint.seen()) != called {
		t.Errorf("the guard let a request through: %+v", endpoint.seen()[called:])
	}
}

// Two accounts, each with an OpenAI-compatible connection: neither lists,
// tests, refreshes or removes the other's, and the answer is "not found".
func TestOneAccountCannotReachAnothersEndpoint(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	endpoint := newStub(t, "m")
	keys := seal(t)
	herKey, _ := keys.Seal(alice, "fake-good-key")
	testpostgres.Exec(t, pool, `
INSERT INTO connections (id, provider, base_url, api_key, extra, scope, user_id) VALUES
 (1, 'openai_compatible', '`+endpoint.URL+`/v1', '`+herKey+`', '{}', 'user', '`+alice+`');
INSERT INTO models (id, connection_id, model_id) VALUES (10, 1, 'm');`)
	his := ownRoutes(t, pool, loopbackEndpoints(), sessionOf(bob))
	for _, call := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/model-connections/1/verify"},
		{http.MethodPost, "/api/v1/model-connections/1/discover"},
		{http.MethodPost, "/api/v1/models/10/test"},
		{http.MethodDelete, "/api/v1/model-connections/1"},
	} {
		if status, body := send(t, his, call.method, call.path, ""); status != http.StatusNotFound || strings.Contains(body, "fake-") {
			t.Errorf("Bob %s %s = %d %s, want a 404", call.method, call.path, status, body)
		}
	}
	if seen := endpoint.seen(); len(seen) != 0 {
		t.Fatalf("Bob reached Alice's endpoint: %+v", seen)
	}
	if _, listed := send(t, his, http.MethodGet, ConnectionsPath, ""); listed != "[]" {
		t.Errorf("Bob's list: %s", listed)
	}
}
