package modelconnections

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// costMap is a cut of the proxy's catalogue in its own shape: models of
// four providers, a price that is named like a model, a model for speech,
// one filed behind its provider's prefix, the catalogue's own sample
// entry, and two providers no connection is made to through a catalogue.
const costMap = `{
 "sample_spec": {"litellm_provider": "one of https://docs.litellm.ai/docs/providers", "max_input_tokens": "a number", "mode": "one of: chat, embedding"},
 "gpt-test": {"litellm_provider": "openai", "mode": "chat", "max_input_tokens": 128000, "max_output_tokens": 16384, "supports_vision": true, "supports_function_calling": true},
 "gpt-test-pro": {"litellm_provider": "openai", "mode": "responses", "max_input_tokens": 200000},
 "paint-test": {"litellm_provider": "openai", "mode": "image_generation"},
 "high/1024-x-1024/paint-test": {"litellm_provider": "openai", "mode": "image_generation"},
 "speak-test": {"litellm_provider": "openai", "mode": "audio_speech"},
 "old-complete": {"litellm_provider": "text-completion-openai", "mode": "completion"},
 "claude-test": {"litellm_provider": "anthropic", "mode": "chat", "max_input_tokens": 200000, "supports_vision": true},
 "anthropic.claude-test-v1:0": {"litellm_provider": "bedrock", "mode": "chat", "max_input_tokens": 200000},
 "amazon.nova-test-v1:0": {"litellm_provider": "bedrock_converse", "mode": "chat", "max_input_tokens": 300000},
 "gemini-test": {"litellm_provider": "vertex_ai-language-models", "mode": "chat", "max_input_tokens": 1048576},
 "vertex_ai/claude-test": {"litellm_provider": "vertex_ai-anthropic_models", "mode": "chat", "max_input_tokens": 200000},
 "vertex_ai/gemini-test": {"litellm_provider": "vertex_ai", "mode": "chat", "max_input_tokens": 5},
 "openrouter/openai/gpt-test": {"litellm_provider": "openrouter", "mode": "chat"},
 "azure/gpt-test": {"litellm_provider": "azure", "mode": "chat"}
}`

func modelIDs(models []pickedModel) string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ModelID)
	}
	return strings.Join(ids, " ")
}

// The list of a provider's models is asked of the model proxy and of
// nothing else: its catalogue, at the proxy's root, read once and kept.
func TestTheCatalogueIsTheModelProxys(t *testing.T) {
	var asked atomic.Int64
	answer := atomic.Value{}
	answer.Store(costMap)
	proxy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		asked.Add(1)
		if request.URL.Path != "/public/litellm_model_cost_map" || request.URL.Query().Get("catalog_only") != "true" {
			t.Errorf("the proxy was asked %s, not for its catalogue alone", request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer fake-proxy-key" {
			t.Errorf("the request carried %q, not the deployment's key", request.Header.Get("Authorization"))
		}
		body := answer.Load().(string)
		if body == "" {
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(response, body)
	}))
	defer proxy.Close()
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	catalogue := &ProxyCatalogue{
		// As configured: the OpenAI-compatible root. The catalogue is
		// beside /v1, not under it.
		URL: proxy.URL + "/v1/", Key: "fake-proxy-key", HTTP: proxy.Client(),
		Timeout: 5 * time.Second, Fresh: time.Hour, Now: func() time.Time { return now },
	}
	if err := catalogue.Check(); err != nil {
		t.Fatal(err)
	}

	for provider, want := range map[string]string{
		// The price, the speech model and the completion model are not
		// models a connection is given; each list is in id order.
		"openai":    "gpt-test gpt-test-pro paint-test",
		"anthropic": "claude-test",
		"bedrock":   "amazon.nova-test-v1:0 anthropic.claude-test-v1:0",
		// Filed under three family names and behind a prefix; the model
		// named twice once the prefix is off is listed once.
		"vertex_ai": "claude-test gemini-test",
	} {
		models, err := catalogue.Models(context.Background(), provider)
		if err != nil || modelIDs(models) != want {
			t.Errorf("%s: %q, %v; want %q", provider, modelIDs(models), err, want)
		}
	}
	if asked.Load() != 1 {
		t.Errorf("the proxy was asked %d times for four lists; the catalogue is read once and kept", asked.Load())
	}

	models, _ := catalogue.Models(context.Background(), "openai")
	chat, image := models[0], models[2]
	if !*chat.SupportsChat || *chat.SupportsImageGeneration || !*chat.SupportsImageInput || !*chat.SupportsTools ||
		*chat.MaxInputTokens != 128000 || chat.Source != "DISCOVERED" ||
		string(chat.Catalog) != `{"litellm_provider":"openai","max_context_length":128000,"max_output_tokens":16384,"mode":"chat","source":"proxy_catalogue"}` {
		t.Errorf("a chat model was read as %+v %s", chat, chat.Catalog)
	}
	// What the catalogue does not state stays unknown, never false.
	if *image.SupportsChat || !*image.SupportsImageGeneration || image.SupportsTools != nil || image.MaxInputTokens != nil {
		t.Errorf("an image model was read as %+v", image)
	}

	// A provider at an address of the person's own is in no catalogue,
	// and the proxy is not asked.
	for _, provider := range []string{"openai_compatible", "ollama_chat", "lm_studio", "azure", "openrouter", "requesty"} {
		if _, err := catalogue.Models(context.Background(), provider); !errors.Is(err, ErrNoCatalogue) {
			t.Errorf("%s: %v, want ErrNoCatalogue", provider, err)
		}
	}
	if asked.Load() != 1 {
		t.Errorf("the proxy was asked about a provider it has no catalogue for")
	}

	// Once the kept copy is old it is read again, and a proxy that cannot
	// answer is an error, not yesterday's list passed off as today's.
	now = now.Add(time.Hour)
	answer.Store("")
	if _, err := catalogue.Models(context.Background(), "openai"); !errors.Is(err, ErrCatalogueUnreadable) {
		t.Errorf("a proxy answering 500: %v, want ErrCatalogueUnreadable", err)
	}
	answer.Store(`[]`)
	if _, err := catalogue.Models(context.Background(), "openai"); !errors.Is(err, ErrCatalogueUnreadable) {
		t.Errorf("a catalogue that is not an object: %v, want ErrCatalogueUnreadable", err)
	}
	answer.Store(`{"gpt-next": {"litellm_provider": "openai", "mode": "chat"}}`)
	if models, err := catalogue.Models(context.Background(), "openai"); err != nil || modelIDs(models) != "gpt-next" {
		t.Errorf("after the proxy came back: %q, %v", modelIDs(models), err)
	}
}

// The provider list tells the settings screen which providers have a list
// to refresh.
func TestTheProviderListSaysWhereModelsAreListedFrom(t *testing.T) {
	status, body := get(t, served(nil, principal.Principal{UserID: testpostgres.Alice, Method: principal.MethodSession}), ProvidersPath)
	if status != http.StatusOK {
		t.Fatalf("%d %s", status, body)
	}
	for _, piece := range []string{
		`{"provider":"openai","transport":"NATIVE","discovery":"proxy_catalogue"`,
		`{"provider":"ollama_chat","transport":"OLLAMA","discovery":"none"`,
		`{"provider":"azure","transport":"NATIVE","discovery":"none"`,
		// Listed from the endpoint itself, and the one any account adds.
		`{"provider":"openai_compatible","transport":"OPENAI_COMPATIBLE","discovery":"endpoint"`,
		`"local_only":false,"any_account":true}`,
	} {
		if !strings.Contains(body, piece) {
			t.Errorf("the provider list lacks %s: %s", piece, body)
		}
	}
}

// shelf is a Catalogue that answers from a fixed list and counts.
type shelf struct {
	models map[string][]pickedModel
	down   bool
	asked  int
}

func (s *shelf) Models(_ context.Context, provider string) ([]pickedModel, error) {
	if !HasCatalogue(provider) {
		return nil, ErrNoCatalogue
	}
	s.asked++
	if s.down {
		return nil, ErrCatalogueUnreadable
	}
	return s.models[provider], nil
}

// modelRoutes is every route of the package over a real database, behind
// the body reader, as one caller.
func modelRoutes(t *testing.T, pool *pgxpool.Pool, prober Prober, catalogue Catalogue, caller principal.Principal) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	store := Store{Pool: pool}
	routes := WriteRoutes{Connections: store, Keys: seal(t), Prober: prober, Catalogue: catalogue, Endpoints: loopbackEndpoints(), Errors: envelope, Logger: logger}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	routes.Register(api)
	GlobalRoutes{Connections: store, Errors: envelope, Logger: logger}.Register(api)
	bodies := requestbody.Middleware{Covers: func(*http.Request) bool { return true }}
	return bodies.Wrap(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), caller)))
	}))
}

func testShelf() *shelf {
	yes, no := true, false
	limit := int32(128000)
	return &shelf{models: map[string][]pickedModel{"openai": {
		{ModelID: "gpt-test", Source: "DISCOVERED", SupportsChat: &yes, SupportsImageGeneration: &no, SupportsTools: &yes,
			MaxInputTokens: &limit, Catalog: []byte(`{"source":"proxy_catalogue"}`)},
		{ModelID: "her-model", Source: "DISCOVERED", SupportsChat: &yes, SupportsImageGeneration: &no,
			MaxInputTokens: &limit, Catalog: []byte(`{"source":"proxy_catalogue"}`)},
	}}}
}

// The isolation test of a connection's models: verifying, refreshing,
// switching and editing are the owner's alone, and the operator's
// connection is nobody's to change.
func TestAConnectionsModelsAreItsOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	administrators(t, pool)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	herKey, _ := seal(t).Seal(alice, "fake-alice-key")
	testpostgres.Exec(t, pool, `
INSERT INTO connections (id, provider, base_url, api_key, extra, scope, user_id) VALUES
 (1, 'openai', NULL, '`+herKey+`', '{}', 'user', '`+alice+`'),
 (2, 'openai', NULL, NULL, '{}', 'user', '`+bob+`'),
 (3, 'openai', 'http://proxy.internal.test:4000', 'fake-site-key', '{"litellm_params":{"token":"fake-site-bag"}}', 'global', NULL),
 (4, 'ollama_chat', 'http://ollama.example.test:11434', NULL, '{}', 'user', '`+alice+`'),
 (5, 'anthropic', NULL, NULL, '{}', 'user', '`+alice+`');
INSERT INTO models (id, connection_id, model_id, source, supports_chat, supports_tools, max_input_tokens, enabled) VALUES
 (10, 1, 'her-model',  'MANUAL', NULL, false, 4000, true),
 (11, 1, 'her-images', 'MANUAL', false, NULL, NULL, true),
 (20, 2, 'his-model',  'MANUAL', true, NULL, NULL, true),
 (30, 3, 'site-model', 'DISCOVERED', true, NULL, NULL, true);
SELECT setval('models_id_seq', 100);`)
	tried, catalogue := &recorder{}, testShelf()
	hers, his := modelRoutes(t, pool, tried, catalogue, sessionOf(alice)), modelRoutes(t, pool, tried, catalogue, sessionOf(bob))
	var before string
	snapshot := func() (rows string) {
		t.Helper()
		if err := pool.QueryRow(context.Background(), `SELECT string_agg(models::text, ';' ORDER BY id) FROM models`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	before = snapshot()

	// Bob against Alice's connection (1), the operator's (3) and one that
	// does not exist (999): the same 404 for all three, and for their
	// models. An administrator's flag does not reach the operator's
	// either: both accounts are administrators here.
	for _, id := range []string{"1", "3", "999"} {
		for _, call := range []struct{ method, path, body string }{
			{http.MethodPost, "/api/v1/model-connections/" + id + "/verify", ``},
			{http.MethodPost, "/api/v1/model-connections/" + id + "/discover", ``},
			{http.MethodPatch, "/api/v1/model-connections/" + id + "/models", `{"model_ids":[10,11,30],"enabled":false}`},
		} {
			if status, body := send(t, his, call.method, call.path, call.body); status != http.StatusNotFound || strings.Contains(body, "fake-") {
				t.Errorf("Bob %s %s = %d %s, want a 404", call.method, call.path, status, body)
			}
		}
	}
	for _, model := range []string{"10", "30", "999"} {
		if status, body := send(t, his, http.MethodPut, "/api/v1/models/"+model, `{"enabled":false,"display_name":"Bob was here"}`); status != http.StatusNotFound {
			t.Errorf("Bob changing model %s = %d %s, want a 404", model, status, body)
		}
	}
	if status, body := send(t, hers, http.MethodPut, "/api/v1/models/30", `{"enabled":false}`); status != http.StatusNotFound {
		t.Errorf("Alice changing the operator's model = %d %s, want a 404", status, body)
	}
	// His own connection, naming her models: nothing of hers is switched.
	if status, body := send(t, his, http.MethodPatch, "/api/v1/model-connections/2/models", `{"model_ids":[10,11,30]}`+""); status != http.StatusUnprocessableEntity {
		t.Errorf("a switch that does not say which way = %d %s, want a 422", status, body)
	}
	if status, body := send(t, his, http.MethodPatch, "/api/v1/model-connections/2/models", `{"model_ids":[10,11,30],"enabled":false}`); status != http.StatusOK || body != "[]" {
		t.Errorf("Bob switching her models through his connection = %d %s, want an empty list", status, body)
	}
	if len(tried.probes) != 0 || catalogue.asked != 0 || snapshot() != before {
		t.Fatalf("Bob reached another account's models: %d tried, %d listed, rows %s", len(tried.probes), catalogue.asked, snapshot())
	}

	// Verify tries the connection with its own key and one of its own
	// models: the chat model, not the image model.
	status, body := send(t, hers, http.MethodPost, "/api/v1/model-connections/1/verify", ``)
	if probe := tried.last(t); status != http.StatusOK || !strings.Contains(body, `"ok":true`) ||
		probe.ModelID != "her-model" || probe.Key != "fake-alice-key" || probe.Provider != "openai" {
		t.Errorf("her verify = %d %s, tried %+v", status, body, probe)
	}
	// A connection with no model cannot be tried, and says so.
	status, body = send(t, hers, http.MethodPost, "/api/v1/model-connections/5/verify", ``)
	if status != http.StatusOK || !strings.Contains(body, `"status":"NO_MODEL","ok":false`) || len(tried.probes) != 1 {
		t.Errorf("verifying a connection with no model = %d %s (%d tried)", status, body, len(tried.probes))
	}

	// A refresh stores what the catalogue lists. The new model arrives
	// switched off; hers keeps its name, source, switch and the limit she
	// set, takes the capability the catalogue states and keeps the one it
	// does not.
	status, body = send(t, hers, http.MethodPost, "/api/v1/model-connections/1/discover", ``)
	if status != http.StatusOK {
		t.Fatalf("her refresh = %d %s", status, body)
	}
	for _, piece := range []string{
		`"id":10,"connection_id":1,"model_id":"her-model","display_name":null,"source":"MANUAL","supports_chat":true,"max_input_tokens":4000,"supports_image_input":null,"supports_tools":false,"supports_image_generation":false,"capabilities_override":{},"enabled":true,"catalog":{"source":"proxy_catalogue"}`,
		`"model_id":"her-images","display_name":null,"source":"MANUAL","supports_chat":false`,
		`"connection_id":1,"model_id":"gpt-test","display_name":null,"source":"DISCOVERED","supports_chat":true,"max_input_tokens":128000,"supports_image_input":null,"supports_tools":true,"supports_image_generation":false,"capabilities_override":{},"enabled":false`,
	} {
		if !strings.Contains(body, piece) {
			t.Errorf("her refresh lacks %s: %s", piece, body)
		}
	}
	if strings.Contains(body, "his-model") || strings.Contains(body, "site-model") {
		t.Errorf("her refresh answered a model that is not hers: %s", body)
	}
	// A second refresh adds nothing.
	if _, again := send(t, hers, http.MethodPost, "/api/v1/model-connections/1/discover", ``); strings.Count(again, `"model_id"`) != 3 {
		t.Errorf("a second refresh = %s, want the same three models", again)
	}
	// Her own server is in no catalogue: said, with what to do instead.
	status, body = send(t, hers, http.MethodPost, "/api/v1/model-connections/4/discover", ``)
	if status != http.StatusConflict || !strings.Contains(body, "Add each model by its name") {
		t.Errorf("refreshing a provider with no catalogue = %d %s, want a 409 that says what to do", status, body)
	}
	catalogue.down = true
	status, body = send(t, hers, http.MethodPost, "/api/v1/model-connections/1/discover", ``)
	if status != http.StatusBadGateway || strings.Contains(body, "127.0.0.1") {
		t.Errorf("a refresh with the proxy down = %d %s, want a 502", status, body)
	}
	catalogue.down = false

	// The preview reads the provider and nothing else of the body.
	status, body = send(t, hers, http.MethodPost, "/api/v1/model-connections/discover-preview", `{"provider":"openai","api_key":"fake-typed-key","scope":"user"}`)
	if status != http.StatusOK || !strings.HasPrefix(body, `[{"model_id":"gpt-test","display_name":null,"source":"DISCOVERED","supports_chat":true,"max_input_tokens":128000,"supports_image_input":null,"supports_tools":true,"supports_image_generation":false,"enabled":false,"metadata":{"source":"proxy_catalogue"}}`) {
		t.Errorf("the preview = %d %s", status, body)
	}
	for sent, want := range map[string]int{
		`{"provider":"lm_studio","base_url":"http://lm.example.test:1234/v1"}`: http.StatusConflict,
		`{"provider":"made-up"}`: http.StatusUnprocessableEntity,
		`{}`:                     http.StatusUnprocessableEntity,
	} {
		if status, body := send(t, hers, http.MethodPost, "/api/v1/model-connections/discover-preview", sent); status != want {
			t.Errorf("a preview of %s = %d %s, want %d", sent, status, body, want)
		}
	}

	// Switching answers the models it changed, by id, and no others.
	status, body = send(t, hers, http.MethodPatch, "/api/v1/model-connections/1/models", `{"model_ids":[11,10,20,30],"enabled":false}`)
	if status != http.StatusOK || strings.Count(body, `"enabled":false`) != 2 || strings.Index(body, `"id":10,`) > strings.Index(body, `"id":11,`) ||
		strings.Contains(body, "his-model") || strings.Contains(body, "site-model") {
		t.Errorf("her switch = %d %s", status, body)
	}
	var others int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM models WHERE id IN (20, 30) AND enabled`).Scan(&others); err != nil || others != 2 {
		t.Errorf("her switch reached %d of the two models that are not hers (%v)", 2-others, err)
	}

	// An edit changes what it names: null clears, an absent field stays.
	status, body = send(t, hers, http.MethodPut, "/api/v1/models/10",
		`{"display_name":"  Hers  ","enabled":true,"max_input_tokens":null,"supports_tools":null,"capabilities_override":{"chat":true}}`)
	if status != http.StatusOK || !strings.Contains(body, `"id":10,"connection_id":1,"model_id":"her-model","display_name":"Hers","source":"MANUAL","supports_chat":true,"max_input_tokens":null,"supports_image_input":null,"supports_tools":null,"supports_image_generation":false,"capabilities_override":{"chat":true},"enabled":true`) {
		t.Errorf("her edit = %d %s", status, body)
	}
	for sent, field := range map[string]string{
		`{"max_input_tokens":0}`:            "max_input_tokens",
		`{"max_input_tokens":1.5}`:          "max_input_tokens",
		`{"enabled":null}`:                  "enabled",
		`{"supports_chat":"yes"}`:           "supports_chat",
		`{"capabilities_override":[1]}`:     "capabilities_override",
		`{"display_name":{"a":1}}`:          "display_name",
		`{"model_ids":[],"enabled":true}`:   "",
		`{"model_ids":"10","enabled":true}`: "",
	} {
		path, method := "/api/v1/models/10", http.MethodPut
		if field == "" {
			path, method, field = "/api/v1/model-connections/1/models", http.MethodPatch, "model_ids"
		}
		if status, body := send(t, hers, method, path, sent); status != http.StatusUnprocessableEntity || !strings.Contains(body, field) {
			t.Errorf("%s %s = %d %s, want a 422 naming %s", method, sent, status, body, field)
		}
	}
}

// The model routes of a connection to a provider other than an
// OpenAI-compatible endpoint are an administrator's in a session, like the
// writes they sit beside (an OpenAI-compatible one is anyone's:
// own_connections_test.go).
func TestOnlyAnAdministratorInASessionManagesModels(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice = testpostgres.Alice
	testpostgres.Exec(t, pool, `
INSERT INTO connections (id, provider, scope, user_id) VALUES (1, 'openai', 'user', '`+alice+`');
INSERT INTO models (id, connection_id, model_id) VALUES (10, 1, 'her-model');`)
	tried, catalogue := &recorder{}, testShelf()
	calls := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/model-connections/1/verify", ``},
		{http.MethodPost, "/api/v1/model-connections/1/discover", ``},
		{http.MethodPost, "/api/v1/model-connections/discover-preview", `{"provider":"openai"}`},
		{http.MethodPatch, "/api/v1/model-connections/1/models", `{"model_ids":[10],"enabled":false}`},
		{http.MethodPut, "/api/v1/models/10", `{"enabled":false}`},
	}
	// Her own connection, and she is not an administrator.
	ordinary := modelRoutes(t, pool, tried, catalogue, sessionOf(alice))
	for _, call := range calls {
		if status, body := send(t, ordinary, call.method, call.path, call.body); status != http.StatusForbidden || !strings.Contains(body, administratorRequired) {
			t.Errorf("an ordinary account %s %s = %d %s, want a 403", call.method, call.path, status, body)
		}
	}
	// An administrator, with an API key and not a session.
	administrators(t, pool)
	keyed := modelRoutes(t, pool, tried, catalogue, principal.Principal{UserID: alice, Method: principal.MethodPersonalAccessToken})
	for _, call := range calls {
		if status, body := send(t, keyed, call.method, call.path, call.body); status != http.StatusForbidden || !strings.Contains(body, sessionRequired) {
			t.Errorf("an API key %s %s = %d %s, want a 403", call.method, call.path, status, body)
		}
	}
	var untouched bool
	if err := pool.QueryRow(context.Background(), `SELECT enabled AND (SELECT count(*) FROM models) = 1 FROM models WHERE id = 10`).Scan(&untouched); err != nil || !untouched ||
		len(tried.probes) != 0 || catalogue.asked != 0 {
		t.Errorf("a refused caller changed, tried or listed something (%v, %d tried, %d listed)", err, len(tried.probes), catalogue.asked)
	}
}

// What the site offers every account is the operator's connections and
// their models, without the operator's address, `extra` or key.
func TestTheSitesOwnConnectionsCarryNoAddressAndNoCredential(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	routes := modelRoutes(t, pool, &recorder{}, testShelf(), sessionOf(bob))
	if status, body := send(t, routes, http.MethodGet, GlobalStatusPath, ``); status != http.StatusOK || body != `{"exists":false}` {
		t.Errorf("a site with no connection = %d %s", status, body)
	}
	if status, body := send(t, routes, http.MethodGet, GlobalConnectionsPath, ``); status != http.StatusOK || body != `[]` {
		t.Errorf("a site with no connection lists %d %s", status, body)
	}
	testpostgres.Exec(t, pool, `
INSERT INTO connections (id, provider, base_url, api_key, extra, scope, enabled, user_id) VALUES
 (1, 'openai', 'https://hers.example.test/v1', 'fake-alice-key', '{}', 'user', true, '`+alice+`'),
 (3, 'openai', 'http://proxy.internal.test:4000', 'fake-site-key', '{"litellm_params":{"token":"fake-site-bag"},"region":"eu"}', 'global', false, NULL);
INSERT INTO models (id, connection_id, model_id, supports_chat) VALUES (10, 1, 'her-model', true), (30, 3, 'site-model', true);`)
	// Switched off: listed as it is, and not something an account can use.
	if status, body := send(t, routes, http.MethodGet, GlobalStatusPath, ``); status != http.StatusOK || body != `{"exists":false}` {
		t.Errorf("a site whose only connection is off = %d %s", status, body)
	}
	testpostgres.Exec(t, pool, `UPDATE connections SET enabled = true WHERE id = 3`)
	if status, body := send(t, routes, http.MethodGet, GlobalStatusPath, ``); status != http.StatusOK || body != `{"exists":true}` {
		t.Errorf("a site with a connection = %d %s", status, body)
	}
	status, body := send(t, routes, http.MethodGet, GlobalConnectionsPath, ``)
	if status != http.StatusOK || !strings.HasPrefix(body, `[{"id":3,"provider":"openai","base_url":null,"extra":{},"scope":"global","user_id":null,"enabled":true,"has_api_key":true,"header_names":[],"models":[{"id":30,"connection_id":3,"model_id":"site-model"`) {
		t.Errorf("the site's connections = %d %s", status, body)
	}
	for _, withheld := range []string{"fake-", "proxy.internal", "region", "her-model", "hers.example"} {
		if strings.Contains(body, withheld) {
			t.Errorf("the site's connections answered %q: %s", withheld, body)
		}
	}
	// The apps' own screens only: an API key is refused.
	keyed := modelRoutes(t, pool, &recorder{}, testShelf(), principal.Principal{UserID: bob, Method: principal.MethodPersonalAccessToken})
	for _, path := range []string{GlobalConnectionsPath, GlobalStatusPath} {
		if status, body := send(t, keyed, http.MethodGet, path, ``); status != http.StatusForbidden {
			t.Errorf("an API key reading %s = %d %s, want a 403", path, status, body)
		}
	}
}
