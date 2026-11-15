package websearchconnect

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// The tests run over the real statements: the variable names a throwaway
// Postgres (testpostgres empties it).
const databaseVariable = "SOLOCLEF_API_TEST_WEBSEARCHCONNECT_DATABASE"

// verifier is the provider: it accepts the keys it is told to, and records
// what it was asked.
type verifier struct {
	answers map[string]error
	asked   []string
}

func (v *verifier) Verify(_ context.Context, provider websearch.Provider, key string) error {
	v.asked = append(v.asked, string(provider)+":"+key)
	return v.answers[key]
}

type harness struct {
	t        *testing.T
	handler  http.Handler
	caller   principal.Principal
	verifier *verifier
	seal     sealedsecret.Seal
	routes   Routes
}

func newHarness(t *testing.T, rows Rows) *harness {
	t.Helper()
	seal, err := sealedsecret.New("fake-deployment-secret", sealedsecret.WebSearchKey)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, seal: seal, verifier: &verifier{answers: map[string]error{
		"fake-rejected-key-0000": websearch.ErrKeyRejected, "fake-spent-key-000000": websearch.ErrQuota,
		"fake-unreachable-0000": websearch.ErrUnavailable,
	}}, caller: principal.Principal{UserID: testpostgres.Alice, Method: principal.MethodSession}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.routes = Routes{Rows: rows, Verifier: h.verifier, Seal: seal, Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}, Logger: logger}
	if err := h.routes.Check(); err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	h.routes.Register(api)
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		api.Adapter().ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), h.caller)))
	})
	bodies := requestbody.Middleware{Covers: requestbody.CoveringRoutes(BodyRoutes()...)}
	h.handler = requestcontext.Middleware(logger, bodies.Wrap(withCaller))
	return h
}

func (h *harness) send(method, path, body string) (int, string) {
	h.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	answered := recorder.Body.String()
	// No answer ever carries a key, in the clear or sealed.
	if strings.Contains(answered, "fake-") || strings.Contains(answered, "v1:") {
		h.t.Fatalf("%s %s answered a key: %s", method, path, answered)
	}
	return recorder.Code, answered
}

const exaPath = "/api/v1/connectors/web-search/exa"

func TestConnectingAndDisconnectingOverTheRealStatements(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	h := newHarness(t, Store{Pool: pool})
	ctx := context.Background()

	if status, answered := h.send(http.MethodGet, ListPath, ""); status != http.StatusOK ||
		answered != `{"providers":[{"provider":"exa","connected":false,"key_mask":null,"connected_at":null},{"provider":"tavily","connected":false,"key_mask":null,"connected_at":null}]}` {
		t.Fatalf("nothing connected: %d %s", status, answered)
	}

	status, answered := h.send(http.MethodPut, exaPath, `{"api_key": "  fake-exa-key-123456789  "}`)
	if status != http.StatusOK || !strings.Contains(answered, `"provider":"exa","connected":true,"key_mask":"••••6789"`) {
		t.Fatalf("connect: %d %s", status, answered)
	}
	if len(h.verifier.asked) != 1 || h.verifier.asked[0] != "exa:fake-exa-key-123456789" {
		t.Errorf("the provider was asked %v", h.verifier.asked)
	}
	// Stored sealed for its owner, and opened by the very reader the chat's
	// tools use.
	var stored string
	if err := pool.QueryRow(ctx, `SELECT api_key FROM web_search_connections WHERE user_id = $1 AND provider = 'exa'`, testpostgres.Alice).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "fake-") {
		t.Fatalf("the key is stored in the clear: %s", stored)
	}
	key, found, err := websearch.StoredKeys{Pool: pool, Seal: h.seal}.Key(ctx, strings.ToUpper(testpostgres.Alice), websearch.Exa)
	if err != nil || !found || key != "fake-exa-key-123456789" {
		t.Fatalf("the chat reads %q, %v, %v", key, found, err)
	}

	// A new key replaces the old one.
	if status, _ := h.send(http.MethodPut, exaPath, `{"api_key": "fake-exa-key-second-0002"}`); status != http.StatusOK {
		t.Fatalf("replace answered %d", status)
	}
	if key, _, _ := (websearch.StoredKeys{Pool: pool, Seal: h.seal}).Key(ctx, testpostgres.Alice, websearch.Exa); key != "fake-exa-key-second-0002" {
		t.Errorf("after the replacement the key is %q", key)
	}

	// The other account sees nothing of hers and cannot remove it.
	h.caller = principal.Principal{UserID: testpostgres.Bob, Method: principal.MethodSession}
	if _, answered := h.send(http.MethodGet, ListPath, ""); strings.Contains(answered, `"connected":true`) {
		t.Errorf("Bob sees %s", answered)
	}
	if status, _ := h.send(http.MethodDelete, exaPath, ""); status != http.StatusNotFound {
		t.Errorf("Bob's disconnect answered %d", status)
	}

	h.caller = principal.Principal{UserID: testpostgres.Alice, Method: principal.MethodSession}
	if status, _ := h.send(http.MethodDelete, exaPath, ""); status != http.StatusNoContent {
		t.Fatalf("disconnect answered %d", status)
	}
	if _, found, _ := (websearch.StoredKeys{Pool: pool, Seal: h.seal}).Key(ctx, testpostgres.Alice, websearch.Exa); found {
		t.Error("the key is still there")
	}
	if status, _ := h.send(http.MethodDelete, exaPath, ""); status != http.StatusNotFound {
		t.Errorf("a second disconnect answered %d", status)
	}
}

// memoryRows is Rows for the refusals, which must never reach a statement.
type memoryRows struct{ writes int }

func (m *memoryRows) Connections(context.Context, string) ([]Connected, error) { return nil, nil }
func (m *memoryRows) Connect(_ context.Context, _, provider, _, mask string) (Connected, error) {
	m.writes++
	return Connected{Provider: provider, KeyMask: mask}, nil
}
func (m *memoryRows) Disconnect(context.Context, string, string) (bool, error) { return false, nil }

func TestWhatIsRefusedAndNeverStored(t *testing.T) {
	rows := &memoryRows{}
	h := newHarness(t, rows)
	for name, c := range map[string]struct {
		path, body string
		status     int
		says       string
	}{
		"no key":                 {exaPath, `{"api_key": "  "}`, http.StatusUnprocessableEntity, keyRequired},
		"a key with a space":     {exaPath, `{"api_key": "fake key"}`, http.StatusUnprocessableEntity, keyMalformed},
		"a key that is a number": {exaPath, `{"api_key": 5}`, http.StatusUnprocessableEntity, "string"},
		"a provider not offered": {"/api/v1/connectors/web-search/bing", `{"api_key": "k"}`, http.StatusNotFound, unknownProvider},
		"a key Exa refuses":      {exaPath, `{"api_key": "fake-rejected-key-0000"}`, http.StatusUnprocessableEntity, "Exa rejected this key"},
		"a spent account":        {exaPath, `{"api_key": "fake-spent-key-000000"}`, http.StatusUnprocessableEntity, "out of credit"},
		"Exa unreachable":        {exaPath, `{"api_key": "fake-unreachable-0000"}`, http.StatusBadGateway, "could not be reached"},
	} {
		if status, answered := h.send(http.MethodPut, c.path, c.body); status != c.status || !strings.Contains(answered, c.says) {
			t.Errorf("%s: answered %d %s, want %d saying %q", name, status, answered, c.status, c.says)
		}
	}
	// Connecting is a person's decision: an API key or an agent cannot.
	for _, method := range []principal.Method{principal.MethodPersonalAccessToken, principal.MethodAgent} {
		h.caller = principal.Principal{UserID: testpostgres.Alice, Method: method}
		for _, verb := range []string{http.MethodPut, http.MethodDelete} {
			if status, _ := h.send(verb, exaPath, `{"api_key": "fake-exa-key-123456789"}`); status != http.StatusForbidden {
				t.Errorf("%s by %v answered %d", verb, method, status)
			}
		}
	}
	if rows.writes != 0 {
		t.Errorf("%d refused requests were stored", rows.writes)
	}
}
