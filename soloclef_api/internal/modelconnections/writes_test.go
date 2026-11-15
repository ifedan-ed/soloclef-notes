package modelconnections

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// recorder is an Endpoints that remembers what it was asked and answers
// from what the test gave it, so a route test needs no server: Try says
// the model worked, Models lists `models` or fails with `listErr`.
type recorder struct {
	mu      sync.Mutex
	probes  []Probe
	listed  int
	models  []pickedModel
	listErr error
}

func (r *recorder) Try(_ context.Context, probe Probe) Verdict {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes = append(r.probes, probe)
	return Verdict{Status: "OK", OK: true, Message: "Model test succeeded."}
}

func (r *recorder) Models(_ context.Context, probe Probe) ([]pickedModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes = append(r.probes, probe)
	r.listed++
	return r.models, r.listErr
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

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.probes)
}

func seal(t *testing.T) KeySeal {
	t.Helper()
	keys, err := NewKeySeal("fake-deployment-secret")
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// writes is the list, the writes, the tests and the model routes over the
// given statements, behind the body reader, as one caller, with the endpoint
// client given.
func writes(t *testing.T, connections Writer, lister Lister, endpoints Endpoints, caller principal.Principal) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	routes := WriteRoutes{Connections: connections, Keys: seal(t), Endpoints: endpoints, Errors: envelope, Logger: logger}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	ConnectionsRoute{Connections: lister, Errors: envelope, Logger: logger}.Register(api)
	routes.Register(api)
	bodies := requestbody.Middleware{Covers: func(*http.Request) bool { return true }}
	return bodies.Wrap(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), caller)))
	}))
}

// over is writes over a real database.
func over(t *testing.T, pool *pgxpool.Pool, endpoints Endpoints, caller principal.Principal) http.Handler {
	t.Helper()
	store := Store{Pool: pool}
	return writes(t, store, store, endpoints, caller)
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
// account's list untouched. Neither account administers anything: any
// account adds its own endpoint.
func TestMakingAConnection(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	hers := over(t, pool, &recorder{}, sessionOf(alice))

	status, body := send(t, hers, http.MethodPost, ConnectionsPath, `{
		"provider":"openai_compatible","base_url":"https://models.example.test/v1","api_key":"fake-alice-key",
		"extra":{"region":"eu"},"scope":"user",
		"models":[
		 {"model_id":" tiny ","display_name":"Tiny","source":"MANUAL","supports_chat":true,"max_input_tokens":8000,"enabled":true,"metadata":{"family":"t"}},
		 {"model_id":"tiny"},
		 {"model_id":"large"}]}`)
	if status != http.StatusOK {
		t.Fatalf("create = %d %s", status, body)
	}
	if strings.Contains(body, "fake-") {
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
	if _, his := send(t, over(t, pool, &recorder{}, sessionOf(bob)), http.MethodGet, ConnectionsPath, ""); his != "[]" {
		t.Errorf("Bob's list after Alice made a connection: %s", his)
	}
}

// What a write refuses before any row is touched or any endpoint called.
func TestWhatAWriteRefuses(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	tried := &recorder{}
	hers := over(t, pool, tried, sessionOf(testpostgres.Alice))
	const ok = `"provider":"openai_compatible","base_url":"https://models.example.test/v1","api_key":"fake-k"`
	for name, test := range map[string]struct{ method, path, body, want string }{
		"no provider":              {http.MethodPost, ConnectionsPath, `{}`, "provider is required"},
		"an unknown provider":      {http.MethodPost, ConnectionsPath, `{"provider":"made_up"}`, providerUnknown},
		"a provider that is gone":  {http.MethodPost, ConnectionsPath, `{"provider":"bedrock","api_key":"fake-k"}`, providerUnknown},
		"not an object":            {http.MethodPost, ConnectionsPath, `[1]`, "JSON object"},
		"the operator's scope":     {http.MethodPost, ConnectionsPath, `{` + ok + `,"scope":"global"}`, "configured in the model proxy"},
		"a file address":           {http.MethodPost, ConnectionsPath, `{"provider":"openai_compatible","base_url":"file:///etc/passwd","api_key":"fake-k"}`, "http or https"},
		"credentials in a URL":     {http.MethodPost, ConnectionsPath, `{"provider":"openai_compatible","base_url":"https://u:p@models.example.test","api_key":"fake-k"}`, "http or https"},
		"extra a list":             {http.MethodPost, ConnectionsPath, `{` + ok + `,"extra":[]}`, "extra must be an object"},
		"a credential bag":         {http.MethodPost, ConnectionsPath, `{` + ok + `,"extra":{"litellm_params":{"aws_secret_access_key":"fake-s"}}}`, "litellm_params or kwargs"},
		"a blank model id":         {http.MethodPost, ConnectionsPath, `{` + ok + `,"models":[{"model_id":" "}]}`, "model_id"},
		"an unknown source":        {http.MethodPost, ConnectionsPath, `{` + ok + `,"models":[{"model_id":"m","source":"GUESSED"}]}`, "source"},
		"update: null provider":    {http.MethodPut, "/api/v1/model-connections/1", `{"provider":null}`, providerUnknown},
		"update: another provider": {http.MethodPut, "/api/v1/model-connections/1", `{"provider":"openai"}`, providerUnknown},
		"update: null enabled":     {http.MethodPut, "/api/v1/model-connections/1", `{"enabled":null}`, "true or false"},
		"update: id not a number":  {http.MethodPut, "/api/v1/model-connections/abc", `{}`, "connection_id"},
		"test: no model":           {http.MethodPost, TestPreviewPath, `{` + ok + `}`, "model_id is required"},
		"test: another provider":   {http.MethodPost, TestPreviewPath, `{"provider":"anthropic","api_key":"fake-k","model_id":"m"}`, providerUnknown},
	} {
		status, body := send(t, hers, test.method, test.path, test.body)
		if status != http.StatusUnprocessableEntity || !strings.Contains(body, test.want) || strings.Contains(body, "fake-") {
			t.Errorf("%s: %d %s, want a 422 saying %q", name, status, body, test.want)
		}
	}
	var rows int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM connections`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("a refused write stored %d rows, %v", rows, err)
	}
	if tried.count() != 0 {
		t.Errorf("a refused request reached the endpoint: %+v", tried.probes)
	}
}

// A person, in a session, writes and tests connections: an API key and an
// agent acting for the same account are refused before a model is tried
// or anything is stored, administrator or not.
func TestOnlyASessionWrites(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice = testpostgres.Alice
	testpostgres.Exec(t, pool, `UPDATE "user" SET is_superuser = true WHERE id = '`+alice+`';
INSERT INTO connections (id, provider, base_url, scope, user_id) VALUES (1, 'openai_compatible', 'https://hers.example.test/v1', 'user', '`+alice+`');
INSERT INTO models (id, connection_id, model_id) VALUES (10, 1, 'her-model');`)
	tried := &recorder{}
	calls := []struct{ method, path, body string }{
		{http.MethodPost, ConnectionsPath, `{"provider":"openai_compatible","base_url":"https://x.example.test/v1","api_key":"fake-k"}`},
		{http.MethodPut, "/api/v1/model-connections/1", `{"enabled":false}`},
		{http.MethodDelete, "/api/v1/model-connections/1", ``},
		{http.MethodPost, TestPreviewPath, `{"provider":"openai_compatible","base_url":"https://x.example.test/v1","api_key":"fake-k","model_id":"m"}`},
		{http.MethodPost, "/api/v1/model-connections/1/test-update", `{"model_id":"m"}`},
		{http.MethodPost, "/api/v1/models/10/test", ``},
	}
	for name, caller := range map[string]principal.Principal{
		"her API key": {UserID: alice, Method: principal.MethodPersonalAccessToken},
	} {
		handler := over(t, pool, tried, caller)
		for _, call := range calls {
			if status, body := send(t, handler, call.method, call.path, call.body); status != http.StatusForbidden || !strings.Contains(body, sessionRequired) {
				t.Errorf("%s: %s %s = %d %s, want 403", name, call.method, call.path, status, body)
			}
		}
	}
	var connections, enabled int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), count(*) FILTER (WHERE enabled) FROM connections`).Scan(&connections, &enabled); err != nil || connections != 1 || enabled != 1 {
		t.Errorf("after the refusals: %d connections, %d enabled, %v", connections, enabled, err)
	}
	if tried.count() != 0 {
		t.Errorf("a refused caller had a model tried: %+v", tried.probes)
	}
}

// The isolation test of the writes: two accounts, each with a connection.
// Neither can change, remove or test the other's, by its id or by its
// model's id, and each refusal is the "not found" of an id that does not
// exist.
func TestOneAccountCannotWriteAnothersConnection(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	keys := seal(t)
	herKey, _ := keys.Seal(alice, "fake-alice-key")
	herHeaders, err := ownmodels.SealedHeaders(keys, alice, map[string]string{"X-Org": "fake-alice-org"})
	if err != nil {
		t.Fatal(err)
	}
	testpostgres.Exec(t, pool, `
INSERT INTO connections (id, provider, base_url, api_key, extra, scope, user_id) VALUES
 (1, 'openai_compatible', 'https://hers.example.test/v1', '`+herKey+`', '{"region":"eu"}'::jsonb || '`+string(herHeaders)+`'::jsonb, 'user', '`+alice+`'),
 (2, 'openai_compatible', 'https://his.example.test/v1', NULL, '{}', 'user', '`+bob+`'),
 (3, 'openai', NULL, 'fake-site-key', '{}', 'global', NULL);
INSERT INTO models (id, connection_id, model_id) VALUES (10, 1, 'her-model'), (20, 2, 'his-model'), (30, 3, 'site-model');
SELECT setval('connections_id_seq', 100);`)
	tried := &recorder{}
	hers, his := over(t, pool, tried, sessionOf(alice)), over(t, pool, tried, sessionOf(bob))

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
	if tried.count() != 0 {
		t.Fatalf("Bob had another account's model tried: %+v", tried.probes)
	}
	var untouched bool
	if err := pool.QueryRow(context.Background(), `SELECT enabled AND api_key = $1 AND base_url = 'https://hers.example.test/v1'
		AND (SELECT count(*) FROM models WHERE connection_id IN (1, 3)) = 2 AND (SELECT count(*) FROM connections) = 3
		FROM connections WHERE id = 1`, herKey).Scan(&untouched); err != nil || !untouched {
		t.Fatalf("Alice's or the operator's rows changed: %v, %v", untouched, err)
	}

	// Alice testing her stored model: her own key and headers, opened, and
	// her address reach the endpoint, and nothing of them reaches the
	// answer.
	status, body := send(t, hers, http.MethodPost, "/api/v1/models/10/test", "")
	if status != http.StatusOK || body != `{"status":"OK","ok":true,"message":"Model test succeeded."}` {
		t.Fatalf("her model test = %d %s", status, body)
	}
	if got := tried.last(t); got.Key != "fake-alice-key" || got.BaseURL != "https://hers.example.test/v1" || got.ModelID != "her-model" ||
		got.Headers["X-Org"] != "fake-alice-org" {
		t.Errorf("her stored model was tried as %+v", got)
	}

	// A change tested before it is saved: a field the body names is the
	// proposal, a field it leaves out is the stored one, and nothing is
	// stored.
	for name, test := range map[string]struct {
		body string
		want Probe
	}{
		"the saved key, a new address": {`{"model_id":"m","base_url":"https://new.example.test/v1"}`,
			Probe{BaseURL: "https://new.example.test/v1", Key: "fake-alice-key", ModelID: "m", Headers: map[string]string{"X-Org": "fake-alice-org"}}},
		"a new key": {`{"model_id":"m","api_key":"fake-new-key"}`,
			Probe{BaseURL: "https://hers.example.test/v1", Key: "fake-new-key", ModelID: "m", Headers: map[string]string{"X-Org": "fake-alice-org"}}},
		"the headers removed": {`{"model_id":"m","headers":null}`,
			Probe{BaseURL: "https://hers.example.test/v1", Key: "fake-alice-key", ModelID: "m"}},
	} {
		if status, body := send(t, hers, http.MethodPost, "/api/v1/model-connections/1/test-update", test.body); status != http.StatusOK {
			t.Fatalf("%s: %d %s", name, status, body)
		}
		got := tried.last(t)
		if got.BaseURL != test.want.BaseURL || got.Key != test.want.Key || got.ModelID != test.want.ModelID ||
			len(got.Headers) != len(test.want.Headers) || got.Headers["X-Org"] != test.want.Headers["X-Org"] {
			t.Errorf("%s: tried as %+v, want %+v", name, got, test.want)
		}
	}
	if err := pool.QueryRow(context.Background(), `SELECT api_key = $1 AND base_url = 'https://hers.example.test/v1' AND extra ? 'headers_sealed'
		FROM connections WHERE id = 1`, herKey).Scan(&untouched); err != nil || !untouched {
		t.Errorf("a test of a change stored it: %v, %v", untouched, err)
	}

	// Saving a change: `extra` is replaced, the headers she cannot see are
	// kept, and so is the key she did not name.
	status, body = send(t, hers, http.MethodPut, "/api/v1/model-connections/1", `{"extra":{"region":"us"},"base_url":"https://moved.example.test/v1","enabled":false}`)
	if status != http.StatusOK || !strings.Contains(body, `"base_url":"https://moved.example.test/v1","extra":{"region":"us"},`) ||
		!strings.Contains(body, `"enabled":false,"has_api_key":true,"header_names":["X-Org"]`) || strings.Contains(body, "fake-") {
		t.Errorf("her update = %d %s", status, body)
	}
	var extra string
	if err := pool.QueryRow(context.Background(), `SELECT extra::text FROM connections WHERE id = 1 AND api_key = $1`, herKey).Scan(&extra); err != nil ||
		!strings.Contains(extra, "headers_sealed") || !strings.Contains(extra, `"region": "us"`) {
		t.Errorf("after the update extra is %s, %v: the headers must survive a save that could not have sent them", extra, err)
	}
	// The key cannot be removed: this tier calls the endpoint with it, so a
	// connection without one could only fail.
	if status, body = send(t, hers, http.MethodPut, "/api/v1/model-connections/1", `{"api_key":null}`); status != http.StatusUnprocessableEntity || !strings.Contains(body, "needs the endpoint's API key") {
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
	testpostgres.Exec(t, pool, `
INSERT INTO connections (id, provider, base_url, api_key, scope, user_id) VALUES (1, 'openai_compatible', 'https://hers.example.test/v1', 'fake-key-stored-before-sealing', 'user', '`+testpostgres.Alice+`');
INSERT INTO models (id, connection_id, model_id) VALUES (10, 1, 'her-model');`)
	tried := &recorder{}
	hers := over(t, pool, tried, sessionOf(testpostgres.Alice))
	for _, path := range []string{"/api/v1/models/10/test", "/api/v1/model-connections/1/test-update", "/api/v1/model-connections/1/discover"} {
		status, body := send(t, hers, http.MethodPost, path, `{"model_id":"m"}`)
		if status != http.StatusConflict || !strings.Contains(body, "Enter the key again") || strings.Contains(body, "fake-") {
			t.Errorf("POST %s = %d %s", path, status, body)
		}
	}
	if tried.count() != 0 {
		t.Errorf("a model was tried without its key: %+v", tried.probes)
	}
}

// retiredRows is the statements as they answer for a connection stored to
// a provider that is gone (bedrock), with one model: enough to drive every
// route to the point where the provider is read. A statement a route must
// not reach for such a row is not implemented, so reaching it panics.
type retiredRows struct {
	Writer
	deleted bool
}

func (retiredRows) Provider(context.Context, string, int64) (string, bool, error) {
	return "bedrock", true, nil
}

func (retiredRows) ModelProvider(context.Context, string, int64) (string, bool, error) {
	return "bedrock", true, nil
}

func (retiredRows) Credentials(context.Context, string, int64) (StoredCredentials, bool, error) {
	return StoredCredentials{Provider: "bedrock", SealedKey: "fake-sealed", Extra: []byte(`{}`)}, true, nil
}

func (retiredRows) ModelCredentials(context.Context, string, int64) (string, StoredCredentials, bool, error) {
	return "m", StoredCredentials{Provider: "bedrock", SealedKey: "fake-sealed", Extra: []byte(`{}`)}, true, nil
}

func (r *retiredRows) Delete(context.Context, string, int64) (bool, error) {
	r.deleted = true
	return true, nil
}

// A row of a provider that is gone can be removed and nothing else: every
// other route answers the one refusal that says what to do, before the key
// is opened or an endpoint is asked anything.
func TestARowOfAProviderThatIsGoneCanOnlyBeRemoved(t *testing.T) {
	rows, tried := &retiredRows{}, &recorder{}
	handler := writes(t, rows, emptyLister{}, tried, sessionOf(testpostgres.Alice))
	for _, call := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/model-connections/1", `{"enabled":false}`},
		{http.MethodPost, "/api/v1/model-connections/1/test-update", `{"model_id":"m"}`},
		{http.MethodPost, "/api/v1/models/10/test", ``},
		{http.MethodPost, "/api/v1/model-connections/1/verify", ``},
		{http.MethodPost, "/api/v1/model-connections/1/discover", ``},
		{http.MethodPatch, "/api/v1/model-connections/1/models", `{"model_ids":[10],"enabled":false}`},
		{http.MethodPut, "/api/v1/models/10", `{"enabled":false}`},
	} {
		status, body := send(t, handler, call.method, call.path, call.body)
		if status != http.StatusConflict || !strings.Contains(body, providerRetired) || strings.Contains(body, "fake-") {
			t.Errorf("%s %s = %d %s, want a 409 saying the provider is no longer supported", call.method, call.path, status, body)
		}
	}
	if tried.count() != 0 {
		t.Errorf("a retired row reached an endpoint: %+v", tried.probes)
	}
	if status, body := send(t, handler, http.MethodDelete, "/api/v1/model-connections/1", ``); status != http.StatusOK || body != `{"status":"deleted"}` || !rows.deleted {
		t.Errorf("removing it = %d %s", status, body)
	}
}
