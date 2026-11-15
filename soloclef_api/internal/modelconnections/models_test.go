package modelconnections

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// listing is what the stand-in endpoint lists: a model the account has
// not stored and one it has, both with what a list can say of them.
func listing() []pickedModel {
	yes, no := true, false
	limit := int32(128000)
	return []pickedModel{
		{ModelID: "gpt-test", Source: "DISCOVERED", SupportsChat: &yes, SupportsImageGeneration: &no, SupportsTools: &yes,
			MaxInputTokens: &limit, Catalog: []byte(`{"source":"endpoint"}`)},
		{ModelID: "her-model", Source: "DISCOVERED", SupportsChat: &yes, SupportsImageGeneration: &no,
			MaxInputTokens: &limit, Catalog: []byte(`{"source":"endpoint"}`)},
	}
}

// The isolation test of a connection's models: verifying, refreshing,
// switching and editing are the owner's alone, and the operator's
// connection is nobody's to change.
func TestAConnectionsModelsAreItsOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	herKey, _ := seal(t).Seal(alice, "fake-alice-key")
	testpostgres.Exec(t, pool, `
INSERT INTO connections (id, provider, base_url, api_key, extra, scope, user_id) VALUES
 (1, 'openai_compatible', 'https://hers.example.test/v1', '`+herKey+`', '{}', 'user', '`+alice+`'),
 (2, 'openai_compatible', 'https://his.example.test/v1', NULL, '{}', 'user', '`+bob+`'),
 (3, 'openai', 'http://proxy.internal.test:4000', 'fake-site-key', '{"litellm_params":{"token":"fake-site-bag"}}', 'global', NULL),
 (5, 'openai_compatible', 'https://empty.example.test/v1', '`+herKey+`', '{}', 'user', '`+alice+`');
INSERT INTO models (id, connection_id, model_id, source, supports_chat, supports_tools, max_input_tokens, enabled) VALUES
 (10, 1, 'her-model',  'MANUAL', NULL, false, 4000, true),
 (11, 1, 'her-images', 'MANUAL', false, NULL, NULL, true),
 (20, 2, 'his-model',  'MANUAL', true, NULL, NULL, true),
 (30, 3, 'site-model', 'DISCOVERED', true, NULL, NULL, true);
SELECT setval('models_id_seq', 100);`)
	tried := &recorder{models: listing()}
	hers, his := over(t, pool, tried, sessionOf(alice)), over(t, pool, tried, sessionOf(bob))
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
	// models.
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
	if tried.count() != 0 || snapshot() != before {
		t.Fatalf("Bob reached another account's models: %d asked, rows %s", tried.count(), snapshot())
	}

	// Verify tries the connection with its own key and one of its own
	// models: the chat model, not the image model.
	status, body := send(t, hers, http.MethodPost, "/api/v1/model-connections/1/verify", ``)
	if probe := tried.last(t); status != http.StatusOK || !strings.Contains(body, `"ok":true`) ||
		probe.ModelID != "her-model" || probe.Key != "fake-alice-key" || probe.BaseURL != "https://hers.example.test/v1" {
		t.Errorf("her verify = %d %s, tried %+v", status, body, probe)
	}
	// A connection with no model cannot be tried, and says so.
	status, body = send(t, hers, http.MethodPost, "/api/v1/model-connections/5/verify", ``)
	if status != http.StatusOK || !strings.Contains(body, `"status":"NO_MODEL","ok":false`) || tried.count() != 1 {
		t.Errorf("verifying a connection with no model = %d %s (%d tried)", status, body, tried.count())
	}

	// A refresh stores what the endpoint lists, asked with her own key.
	// The new model arrives switched off; hers keeps its name, source,
	// switch and the limit she set, takes the capability the list states
	// and keeps the one it does not.
	status, body = send(t, hers, http.MethodPost, "/api/v1/model-connections/1/discover", ``)
	if status != http.StatusOK || tried.last(t).Key != "fake-alice-key" {
		t.Fatalf("her refresh = %d %s, asked as %+v", status, body, tried.last(t))
	}
	for _, piece := range []string{
		`"id":10,"connection_id":1,"model_id":"her-model","display_name":null,"source":"MANUAL","supports_chat":true,"max_input_tokens":4000,"supports_image_input":null,"supports_tools":false,"supports_image_generation":false,"capabilities_override":{},"enabled":true,"catalog":{"source":"endpoint"}`,
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
	// An endpoint that will not list is said in its own coded words.
	tried.listErr = &endpointRefusal{http.StatusConflict, codeListUnsupported, "This endpoint does not list its models."}
	status, body = send(t, hers, http.MethodPost, "/api/v1/model-connections/1/discover", ``)
	if status != http.StatusConflict || codeOf(body) != codeListUnsupported {
		t.Errorf("a refresh of an endpoint with no list = %d %s, want its 409", status, body)
	}
	tried.listErr = nil

	// The preview asks the endpoint the body describes, with the key typed.
	status, body = send(t, hers, http.MethodPost, "/api/v1/model-connections/discover-preview",
		`{"provider":"openai_compatible","base_url":"https://typed.example.test/v1","api_key":"fake-typed-key","scope":"user"}`)
	if status != http.StatusOK || !strings.HasPrefix(body, `[{"model_id":"gpt-test","display_name":null,"source":"DISCOVERED","supports_chat":true,"max_input_tokens":128000,"supports_image_input":null,"supports_tools":true,"supports_image_generation":false,"enabled":false,"metadata":{"source":"endpoint"}}`) ||
		strings.Contains(body, "fake-") {
		t.Errorf("the preview = %d %s", status, body)
	}
	if asked := tried.last(t); asked.BaseURL != "https://typed.example.test/v1" || asked.Key != "fake-typed-key" {
		t.Errorf("the preview asked %+v", asked)
	}
	for sent, want := range map[string]int{
		`{"provider":"lm_studio","base_url":"http://lm.example.test:1234/v1"}`:    http.StatusUnprocessableEntity,
		`{"provider":"openai_compatible","base_url":"https://x.example.test/v1"}`: http.StatusUnprocessableEntity,
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

// The model routes are a person's in a session, like the writes they sit
// beside: any account manages its own connection's models, and an API key
// is refused before anything is listed, tried or changed.
func TestOnlyASessionManagesModels(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice = testpostgres.Alice
	herKey, _ := seal(t).Seal(alice, "fake-alice-key")
	testpostgres.Exec(t, pool, `
INSERT INTO connections (id, provider, base_url, api_key, scope, user_id) VALUES (1, 'openai_compatible', 'https://hers.example.test/v1', '`+herKey+`', 'user', '`+alice+`');
INSERT INTO models (id, connection_id, model_id) VALUES (10, 1, 'her-model');`)
	tried := &recorder{models: listing()}
	calls := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/model-connections/1/verify", ``},
		{http.MethodPost, "/api/v1/model-connections/1/discover", ``},
		{http.MethodPost, "/api/v1/model-connections/discover-preview", `{"provider":"openai_compatible","base_url":"https://x.example.test/v1","api_key":"fake-k"}`},
		{http.MethodPatch, "/api/v1/model-connections/1/models", `{"model_ids":[10],"enabled":false}`},
		{http.MethodPut, "/api/v1/models/10", `{"enabled":false}`},
	}
	keyed := over(t, pool, tried, principal.Principal{UserID: alice, Method: principal.MethodPersonalAccessToken})
	for _, call := range calls {
		if status, body := send(t, keyed, call.method, call.path, call.body); status != http.StatusForbidden || !strings.Contains(body, sessionRequired) {
			t.Errorf("an API key %s %s = %d %s, want a 403", call.method, call.path, status, body)
		}
	}
	var untouched bool
	if err := pool.QueryRow(context.Background(), `SELECT enabled AND (SELECT count(*) FROM models) = 1 FROM models WHERE id = 10`).Scan(&untouched); err != nil || !untouched ||
		tried.count() != 0 {
		t.Errorf("a refused caller changed, tried or listed something (%v, %d asked)", err, tried.count())
	}
	// The same calls in her session, though she administers nothing.
	hers := over(t, pool, tried, sessionOf(alice))
	for _, call := range calls {
		if status, body := send(t, hers, call.method, call.path, call.body); status != http.StatusOK {
			t.Errorf("her session %s %s = %d %s, want a 200", call.method, call.path, status, body)
		}
	}
}
