package modelconnections

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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// recorder is a Prober that remembers what it was asked to try and
// answers that it worked.
type recorder struct {
	mu     sync.Mutex
	probes []Probe
}

func (r *recorder) Try(_ context.Context, probe Probe) Verdict {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes = append(r.probes, probe)
	return Verdict{Status: "OK", OK: true, Message: "Model test succeeded."}
}

func (r *recorder) last(t *testing.T) Probe {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.probes) == 0 {
		t.Fatal("nothing was tried")
	}
	return r.probes[len(r.probes)-1]
}

func seal(t *testing.T) KeySeal {
	t.Helper()
	keys, err := NewKeySeal("fake-deployment-secret")
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// writes is the list and the write routes over a real database, behind
// the body reader, as one caller.
func writes(t *testing.T, pool *pgxpool.Pool, prober Prober, caller principal.Principal) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	store := Store{Pool: pool}
	ConnectionsRoute{Connections: store, Errors: envelope, Logger: logger}.Register(api)
	WriteRoutes{Connections: store, Keys: seal(t), Prober: prober, Endpoints: loopbackEndpoints(), Errors: envelope, Logger: logger}.Register(api)
	bodies := requestbody.Middleware{Covers: func(*http.Request) bool { return true }}
	return bodies.Wrap(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), caller)))
	}))
}

func send(t *testing.T, handler http.Handler, method, path, body string) (int, string) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorded := httptest.NewRecorder()
	handler.ServeHTTP(recorded, request)
	return recorded.Code, recorded.Body.String()
}

func administrators(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	testpostgres.Exec(t, pool, `UPDATE "user" SET is_superuser = true`)
}

func sessionOf(userID string) principal.Principal {
	return principal.Principal{UserID: userID, Method: principal.MethodSession}
}

// A sealed key opens for its owner and for nobody else, under this
// deployment's secret and no other, and two seals of one key differ.
func TestTheKeySeal(t *testing.T) {
	keys := seal(t)
	first, err := keys.Seal(testpostgres.Alice, "fake-key")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := keys.Seal(testpostgres.Alice, "fake-key")
	if first == second || strings.Contains(first, "fake-key") || !strings.HasPrefix(first, "v1:") {
		t.Errorf("seals %q and %q", first, second)
	}
	if opened, err := keys.Open(testpostgres.Alice, first); err != nil || opened != "fake-key" {
		t.Errorf("her own seal opened as %q, %v", opened, err)
	}
	other, _ := NewKeySeal("another-deployment")
	for name, attempt := range map[string]func() (string, error){
		"another account":    func() (string, error) { return keys.Open(testpostgres.Bob, first) },
		"another deployment": func() (string, error) { return other.Open(testpostgres.Alice, first) },
		"a damaged seal":     func() (string, error) { return keys.Open(testpostgres.Alice, first[:len(first)-2]+"AA") },
		"a plain key":        func() (string, error) { return keys.Open(testpostgres.Alice, "fake-key") },
		"nothing":            func() (string, error) { return keys.Open(testpostgres.Alice, "v1:") },
	} {
		if opened, err := attempt(); err != ErrSealUnreadable {
			t.Errorf("%s opened as %q, %v", name, opened, err)
		}
	}
	if _, err := NewKeySeal(""); err == nil {
		t.Error("a seal was made with no secret")
	}
}

// Making a connection: the whole answer, the key sealed in the column and
// absent from the answer, the models stored once each, and a second
// account's list untouched.
func TestMakingAConnection(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	administrators(t, pool)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	hers := writes(t, pool, &recorder{}, sessionOf(alice))

	status, body := send(t, hers, http.MethodPost, ConnectionsPath, `{
		"provider":"openai_compatible","base_url":"https://models.example.test/v1","api_key":"fake-alice-key",
		"extra":{"region":"eu","litellm_params":{"aws_secret_access_key":"fake-bag"}},"scope":"user",
		"models":[
		 {"model_id":" tiny ","display_name":"Tiny","source":"MANUAL","supports_chat":true,"max_input_tokens":8000,"enabled":true,"metadata":{"family":"t"}},
		 {"model_id":"tiny"},
		 {"model_id":"large"}]}`)
	if status != http.StatusOK {
		t.Fatalf("create = %d %s", status, body)
	}
	if strings.Contains(body, "fake-") || strings.Contains(body, "litellm_params") {
		t.Fatalf("a credential was answered: %s", body)
	}
	for _, want := range []string{
		`"provider":"openai_compatible","base_url":"https://models.example.test/v1","extra":{"region":"eu"},"scope":"user","user_id":"` + alice + `","enabled":true,"has_api_key":true`,
		`"model_id":"tiny","display_name":"Tiny","source":"MANUAL","supports_chat":true,"max_input_tokens":8000,`,
		`"enabled":true,"catalog":{"family":"t"}`,
		`"model_id":"large","display_name":null,"source":"DISCOVERED","supports_chat":null`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the answer is missing %s:\n%s", want, body)
		}
	}
	if strings.Count(body, `"model_id":"tiny"`) != 1 {
		t.Errorf("a model id given twice was stored twice: %s", body)
	}

	var stored string
	if err := pool.QueryRow(context.Background(), `SELECT api_key FROM connections WHERE user_id = $1`, alice).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "fake-alice-key") {
		t.Fatalf("the key is stored as typed: %q", stored)
	}
	if opened, err := seal(t).Open(alice, stored); err != nil || opened != "fake-alice-key" {
		t.Errorf("the stored key opens as %q, %v", opened, err)
	}

	// The list agrees with the answer, and Bob's is still empty.
	if _, listed := send(t, hers, http.MethodGet, ConnectionsPath, ""); listed != "["+body+"]" {
		t.Errorf("the list differs from the answer to the create:\n%s\n%s", listed, body)
	}
	if _, his := send(t, writes(t, pool, &recorder{}, sessionOf(bob)), http.MethodGet, ConnectionsPath, ""); his != "[]" {
		t.Errorf("Bob's list after Alice made a connection: %s", his)
	}
}

// What a write refuses before any row is touched.
func TestWhatAWriteRefuses(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	administrators(t, pool)
	hers := writes(t, pool, &recorder{}, sessionOf(testpostgres.Alice))
	for name, test := range map[string]struct{ method, path, body, want string }{
		"no provider":             {http.MethodPost, ConnectionsPath, `{}`, "provider is required"},
		"an unknown provider":     {http.MethodPost, ConnectionsPath, `{"provider":"made_up"}`, "one of the providers"},
		"not an object":           {http.MethodPost, ConnectionsPath, `[1]`, "JSON object"},
		"the operator's scope":    {http.MethodPost, ConnectionsPath, `{"provider":"openai","scope":"global"}`, "configured in the model proxy"},
		"a file address":          {http.MethodPost, ConnectionsPath, `{"provider":"openai","base_url":"file:///etc/passwd"}`, "http or https"},
		"credentials in a URL":    {http.MethodPost, ConnectionsPath, `{"provider":"openai","base_url":"https://u:p@models.example.test"}`, "http or https"},
		"extra a list":            {http.MethodPost, ConnectionsPath, `{"provider":"openai","extra":[]}`, "extra must be an object"},
		"a blank model id":        {http.MethodPost, ConnectionsPath, `{"provider":"openai","models":[{"model_id":" "}]}`, "model_id"},
		"an unknown source":       {http.MethodPost, ConnectionsPath, `{"provider":"openai","models":[{"model_id":"m","source":"GUESSED"}]}`, "source"},
		"update: null provider":   {http.MethodPut, "/api/v1/model-connections/1", `{"provider":null}`, "one of the providers"},
		"update: null enabled":    {http.MethodPut, "/api/v1/model-connections/1", `{"enabled":null}`, "true or false"},
		"update: id not a number": {http.MethodPut, "/api/v1/model-connections/abc", `{}`, "connection_id"},
		"test: no model":          {http.MethodPost, TestPreviewPath, `{"provider":"openai"}`, "model_id is required"},
	} {
		status, body := send(t, hers, test.method, test.path, test.body)
		if status != http.StatusUnprocessableEntity || !strings.Contains(body, test.want) {
			t.Errorf("%s: %d %s, want a 422 saying %q", name, status, body, test.want)
		}
	}
	var rows int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM connections`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("a refused write stored %d rows, %v", rows, err)
	}
}

// Only an administrator, in a session, writes or tests a connection to a
// provider other than an OpenAI-compatible endpoint. Everyone else is
// refused before a model is tried or anything is stored.
func TestOnlyAnAdministratorInASessionWrites(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `UPDATE "user" SET is_superuser = true WHERE id = '`+alice+`';
INSERT INTO connections (id, provider, scope, user_id) VALUES (1, 'openai', 'user', '`+bob+`');
INSERT INTO models (id, connection_id, model_id) VALUES (10, 1, 'his-model');`)
	tried := &recorder{}
	calls := []struct{ method, path, body string }{
		{http.MethodPost, ConnectionsPath, `{"provider":"openai"}`},
		{http.MethodPut, "/api/v1/model-connections/1", `{"enabled":false}`},
		{http.MethodDelete, "/api/v1/model-connections/1", ``},
		{http.MethodPost, TestPreviewPath, `{"provider":"openai","model_id":"m"}`},
		{http.MethodPost, "/api/v1/model-connections/1/test-update", `{"model_id":"m"}`},
		{http.MethodPost, "/api/v1/models/10/test", ``},
	}
	for name, caller := range map[string]principal.Principal{
		"an account that is not an administrator": sessionOf(bob),
		"an administrator's API key":              {UserID: alice, Method: principal.MethodPersonalAccessToken},
		"an agent acting as an administrator":     {UserID: alice, Method: principal.MethodAgent},
	} {
		handler := writes(t, pool, tried, caller)
		for _, call := range calls {
			if status, body := send(t, handler, call.method, call.path, call.body); status != http.StatusForbidden {
				t.Errorf("%s: %s %s = %d %s, want 403", name, call.method, call.path, status, body)
			}
		}
	}
	var connections, enabled int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), count(*) FILTER (WHERE enabled) FROM connections`).Scan(&connections, &enabled); err != nil || connections != 1 || enabled != 1 {
		t.Errorf("after the refusals: %d connections, %d enabled, %v", connections, enabled, err)
	}
	if len(tried.probes) != 0 {
		t.Errorf("a refused caller had a model tried: %+v", tried.probes)
	}
}

// The isolation test of the writes: two administrators, each with a
// connection. Neither can change, remove or test the other's, by its id or
// by its model's id, and each refusal is the "not found" of an id that
// does not exist.
func TestOneAccountCannotWriteAnothersConnection(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	administrators(t, pool)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	keys := seal(t)
	herKey, _ := keys.Seal(alice, "fake-alice-key")
	testpostgres.Exec(t, pool, `
INSERT INTO connections (id, provider, base_url, api_key, extra, scope, user_id) VALUES
 (1, 'openai', 'https://hers.example.test/v1', '`+herKey+`', '{"litellm_params":{"token":"fake-alice-bag"},"region":"eu"}', 'user', '`+alice+`'),
 (2, 'openai', NULL, NULL, '{}', 'user', '`+bob+`'),
 (3, 'openai', NULL, 'fake-site-key', '{}', 'global', NULL);
INSERT INTO models (id, connection_id, model_id) VALUES (10, 1, 'her-model'), (20, 2, 'his-model'), (30, 3, 'site-model');
SELECT setval('connections_id_seq', 100);`)
	tried := &recorder{}
	hers, his := writes(t, pool, tried, sessionOf(alice)), writes(t, pool, tried, sessionOf(bob))

	absent := func(status int, body string) bool {
		return status == http.StatusNotFound && !strings.Contains(body, "fake-")
	}
	// Bob against Alice's connection (1), the operator's (3) and one that
	// does not exist (999): the same answer for all three.
	for _, id := range []string{"1", "3", "999"} {
		for _, call := range []struct{ method, path, body string }{
			{http.MethodPut, "/api/v1/model-connections/" + id, `{"enabled":false,"api_key":"fake-bobs-key","base_url":"https://his.example.test"}`},
			{http.MethodPost, "/api/v1/model-connections/" + id + "/test-update", `{"model_id":"m"}`},
			{http.MethodDelete, "/api/v1/model-connections/" + id, ``},
		} {
			if status, body := send(t, his, call.method, call.path, call.body); !absent(status, body) {
				t.Errorf("Bob %s %s = %d %s, want a 404", call.method, call.path, status, body)
			}
		}
	}
	for _, model := range []string{"10", "30", "999"} {
		if status, body := send(t, his, http.MethodPost, "/api/v1/models/"+model+"/test", ""); !absent(status, body) {
			t.Errorf("Bob testing model %s = %d %s, want a 404", model, status, body)
		}
	}
	if len(tried.probes) != 0 {
		t.Fatalf("Bob had another account's model tried: %+v", tried.probes)
	}
	var untouched bool
	if err := pool.QueryRow(context.Background(), `SELECT enabled AND api_key = $1 AND base_url = 'https://hers.example.test/v1'
		AND (SELECT count(*) FROM models WHERE connection_id IN (1, 3)) = 2 AND (SELECT count(*) FROM connections) = 3
		FROM connections WHERE id = 1`, herKey).Scan(&untouched); err != nil || !untouched {
		t.Fatalf("Alice's or the operator's rows changed: %v, %v", untouched, err)
	}

	// Alice testing her stored model: her own key, opened, her address and
	// her bag reach the prober, and nothing of them reaches the answer.
	status, body := send(t, hers, http.MethodPost, "/api/v1/models/10/test", "")
	if status != http.StatusOK || body != `{"status":"OK","ok":true,"message":"Model test succeeded."}` {
		t.Fatalf("her model test = %d %s", status, body)
	}
	if got := tried.last(t); got.Key != "fake-alice-key" || got.BaseURL != "https://hers.example.test/v1" || got.ModelID != "her-model" ||
		got.Provider != "openai" || string(got.Params) != `{"token": "fake-alice-bag"}` {
		t.Errorf("her stored model was tried as %+v (params %s)", got, got.Params)
	}

	// A change tested before it is saved: a field the body names is the
	// proposal, a field it leaves out is the stored one, and nothing is
	// stored.
	for name, test := range map[string]struct {
		body string
		want Probe
	}{
		"the saved key, a new address": {`{"model_id":"m","base_url":"https://new.example.test/v1"}`,
			Probe{Provider: "openai", BaseURL: "https://new.example.test/v1", Key: "fake-alice-key", ModelID: "m"}},
		"a new key": {`{"model_id":"m","api_key":"fake-new-key"}`,
			Probe{Provider: "openai", BaseURL: "https://hers.example.test/v1", Key: "fake-new-key", ModelID: "m"}},
		"the key removed, another provider": {`{"model_id":"m","api_key":null,"provider":"openrouter","base_url":null}`,
			Probe{Provider: "openrouter", ModelID: "m"}},
	} {
		if status, body := send(t, hers, http.MethodPost, "/api/v1/model-connections/1/test-update", test.body); status != http.StatusOK {
			t.Fatalf("%s: %d %s", name, status, body)
		}
		got := tried.last(t)
		got.Params = nil
		if got.Provider != test.want.Provider || got.BaseURL != test.want.BaseURL || got.Key != test.want.Key || got.ModelID != test.want.ModelID {
			t.Errorf("%s: tried as %+v, want %+v", name, got, test.want)
		}
	}
	if err := pool.QueryRow(context.Background(), `SELECT api_key = $1 AND base_url = 'https://hers.example.test/v1' AND provider = 'openai'
		FROM connections WHERE id = 1`, herKey).Scan(&untouched); err != nil || !untouched {
		t.Errorf("a test of a change stored it: %v, %v", untouched, err)
	}

	// Saving a change: `extra` is replaced, the bag she cannot see is kept,
	// the key she did not name is kept, and null removes the address.
	status, body = send(t, hers, http.MethodPut, "/api/v1/model-connections/1", `{"extra":{"region":"us"},"base_url":null,"enabled":false}`)
	if status != http.StatusOK || !strings.Contains(body, `"base_url":null,"extra":{"region":"us"},`) ||
		!strings.Contains(body, `"enabled":false,"has_api_key":true`) || strings.Contains(body, "fake-") {
		t.Errorf("her update = %d %s", status, body)
	}
	var extra string
	if err := pool.QueryRow(context.Background(), `SELECT extra::text FROM connections WHERE id = 1 AND api_key = $1`, herKey).Scan(&extra); err != nil ||
		!strings.Contains(extra, "fake-alice-bag") || !strings.Contains(extra, `"region": "us"`) {
		t.Errorf("after the update extra is %s, %v: the bag must survive a save that could not have sent it", extra, err)
	}
	// Removing the key, then the connection and its models with it.
	if status, body = send(t, hers, http.MethodPut, "/api/v1/model-connections/1", `{"api_key":null}`); status != http.StatusOK || !strings.Contains(body, `"has_api_key":false`) {
		t.Errorf("removing her key = %d %s", status, body)
	}
	if status, body = send(t, hers, http.MethodDelete, "/api/v1/model-connections/1", ""); status != http.StatusOK || body != `{"status":"deleted"}` {
		t.Errorf("her delete = %d %s", status, body)
	}
	if status, body = send(t, hers, http.MethodDelete, "/api/v1/model-connections/1", ""); status != http.StatusNotFound {
		t.Errorf("deleting it twice = %d %s", status, body)
	}
	var left int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM models`).Scan(&left); err != nil || left != 2 {
		t.Errorf("%d models left, want Bob's and the operator's; %v", left, err)
	}
}

// A stored key the deployment's secret no longer opens is said in words a
// person can act on, and no model is tried with nothing.
func TestAStoredKeyThatNoLongerOpens(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	administrators(t, pool)
	testpostgres.Exec(t, pool, `
INSERT INTO connections (id, provider, api_key, scope, user_id) VALUES (1, 'openai', 'fake-key-stored-before-sealing', 'user', '`+testpostgres.Alice+`');
INSERT INTO models (id, connection_id, model_id) VALUES (10, 1, 'her-model');`)
	tried := &recorder{}
	hers := writes(t, pool, tried, sessionOf(testpostgres.Alice))
	for _, path := range []string{"/api/v1/models/10/test", "/api/v1/model-connections/1/test-update"} {
		status, body := send(t, hers, http.MethodPost, path, `{"model_id":"m"}`)
		if status != http.StatusConflict || !strings.Contains(body, "Enter the key again") || strings.Contains(body, "fake-") {
			t.Errorf("POST %s = %d %s", path, status, body)
		}
	}
	if len(tried.probes) != 0 {
		t.Errorf("a model was tried without its key: %+v", tried.probes)
	}
}

// The prober over a stand-in for the model proxy: what it sends, and how
// each of the proxy's answers reads.
func TestTheProxyProber(t *testing.T) {
	var (
		got    map[string]any
		bearer string
		status = http.StatusOK
		reply  = `{"choices":[]}`
	)
	proxy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		bearer = request.Header.Get("Authorization")
		got = nil
		_ = json.NewDecoder(request.Body).Decode(&got)
		if request.URL.Path != "/v1/chat/completions" {
			status = http.StatusTeapot
		}
		response.WriteHeader(status)
		_, _ = io.WriteString(response, reply)
	}))
	defer proxy.Close()
	prober := ProxyProber{URL: proxy.URL + "/v1/", Key: "fake-proxy-key", HTTP: proxy.Client(), Timeout: time.Second}
	if err := prober.Check(); err != nil {
		t.Fatal(err)
	}
	probe := Probe{Provider: "lm_studio", BaseURL: "http://models.example.test/v1", Key: "fake-user-key", ModelID: "tiny",
		Params: json.RawMessage(`{"aws_region_name":"eu","model":"smuggled","api_key":"smuggled"}`)}

	if verdict := prober.Try(context.Background(), probe); verdict != (Verdict{Status: "OK", OK: true, Message: "Model test succeeded."}) {
		t.Errorf("a 200 read as %+v", verdict)
	}
	// The proxy's own key authenticates; the connection's travels in the
	// body, under the provider's proxy prefix, and the bag cannot replace
	// what the request sets itself.
	if bearer != "Bearer fake-proxy-key" || got["model"] != "openai/tiny" || got["api_key"] != "fake-user-key" ||
		got["api_base"] != "http://models.example.test/v1" || got["aws_region_name"] != "eu" {
		t.Errorf("the proxy was sent %v with %q", got, bearer)
	}
	if prober.Try(context.Background(), Probe{Provider: "openai", ModelID: "m"}); got["api_key"] != nil || got["api_base"] != nil {
		t.Errorf("a connection with no key or address sent %v", got)
	}

	for name, test := range map[string]struct {
		status int
		reply  string
		want   Verdict
	}{
		"the provider refuses the key": {401, `{"error":{"message":"Incorrect API key provided: fake-user-key.\n Check it."}}`,
			Verdict{Status: "AUTH_FAILED", Message: "Incorrect API key provided: [key]. Check it."}},
		"no such model":               {404, `{"error":{"message":"model not found"}}`, Verdict{Status: "NOT_FOUND", Message: "model not found"}},
		"the proxy will not use it":   {400, `{"error":{"message":"api_base not allowed in request body"}}`, Verdict{Status: "UNREACHABLE", Message: "api_base not allowed in request body"}},
		"an answer that says nothing": {502, `<html>bad gateway</html>`, Verdict{Status: "UNREACHABLE", Message: "The model test failed."}},
		"a long answer":               {500, `{"error":{"message":"` + strings.Repeat("x", 1000) + `"}}`, Verdict{Status: "UNREACHABLE", Message: strings.Repeat("x", 300) + "…"}},
	} {
		status, reply = test.status, test.reply
		if verdict := prober.Try(context.Background(), probe); verdict != test.want {
			t.Errorf("%s: read as %+v, want %+v", name, verdict, test.want)
		}
	}

	// A proxy that is down: said without its address.
	proxy.Close()
	if verdict := prober.Try(context.Background(), probe); verdict.Status != "UNREACHABLE" || verdict.OK || strings.Contains(verdict.Message, "127.0.0.1") {
		t.Errorf("a proxy that is down read as %+v", verdict)
	}
}
