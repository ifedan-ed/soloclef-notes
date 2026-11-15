package modelconnections

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// served is the two routes on Huma with the caller the gate would have
// resolved put in the context.
func served(connections Lister, caller principal.Principal) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	ProvidersRoute{Errors: envelope, Logger: logger}.Register(api)
	ConnectionsRoute{Connections: connections, Errors: envelope, Logger: logger}.Register(api)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), caller)))
	})
}

func get(t *testing.T, handler http.Handler, path string) (int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder.Code, recorder.Body.String()
}

func render(t *testing.T, rows []ListedConnection) string {
	t.Helper()
	items, err := BuildConnections(rows)
	if err != nil {
		t.Fatal(err)
	}
	body, err := pythonjson.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func text(value string) *string { return &value }

// The whole answer for one connection with one model, so that a field added,
// dropped or moved is seen. There is no billing tier in it: a connection is
// its account's, and nothing is charged.
func TestAConnectionIsRenderedWithItsModels(t *testing.T) {
	made := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	rows := []ListedConnection{{
		ID: 7, Provider: "openai_compatible", BaseURL: text("https://api.example.test/v1"), HasAPIKey: true,
		Extra: []byte(`{"region":"eu"}`), Scope: "user", UserID: text(testpostgres.Alice),
		Enabled: true, CreatedAt: made,
	}}
	chat, tokens := true, int64(128000)
	AttachModels(rows, []ListedModel{
		{ID: 70, ConnectionID: 7, ModelID: "gpt-example", DisplayName: text("Example"), Source: "MANUAL",
			SupportsChat: &chat, MaxInputTokens: &tokens, CapabilitiesOverride: []byte(`{}`),
			Enabled: true, Catalog: []byte(`{"family":"example"}`), CreatedAt: made},
		// A model of a connection that is not in the list is given no home.
		{ID: 71, ConnectionID: 8, ModelID: "stray", Source: "MANUAL",
			CapabilitiesOverride: []byte(`{}`), Catalog: []byte(`{}`), CreatedAt: made},
	})

	want := `[{"id":7,"provider":"openai_compatible","base_url":"https://api.example.test/v1","extra":{"region":"eu"},` +
		`"scope":"user","user_id":"` + testpostgres.Alice + `","enabled":true,"has_api_key":true,"header_names":[],` +
		`"models":[{"id":70,"connection_id":7,"model_id":"gpt-example","display_name":"Example","source":"MANUAL",` +
		`"supports_chat":true,"max_input_tokens":128000,"supports_image_input":null,"supports_tools":null,` +
		`"supports_image_generation":null,"capabilities_override":{},"enabled":true,"catalog":{"family":"example"},` +
		`"created_at":"2026-11-01T10:00:00Z"}],"created_at":"2026-11-01T10:00:00Z"}]`
	if got := render(t, rows); got != want {
		t.Errorf("rendered\n got %s\nwant %s", got, want)
	}
}

// What `extra` holds that is a credential is never answered: the sealed
// headers, and, on a row made before only OpenAI-compatible connections
// were offered, a Bedrock or Vertex AI credential in litellm_params or
// kwargs. Each is left out whole, and only at the top level, where it is
// the key.
func TestTheCredentialBagsOfExtraAreNeverAnswered(t *testing.T) {
	body := render(t, []ListedConnection{{
		ID: 1, Provider: "bedrock", Scope: "user",
		Extra: []byte(`{"litellm_params":{"aws_secret_access_key":"fake-secret"},"kwargs":{"token":"fake-token"},` +
			`"headers_sealed":"fake-sealed","header_names":["X-Org"],` +
			`"region":"eu","nested":{"kwargs":"kept, it is not the bag"}}`),
	}})
	if strings.Contains(body, "fake-") || strings.Contains(body, "litellm_params") || strings.Contains(body, "headers_sealed") {
		t.Fatalf("a credential bag was answered: %s", body)
	}
	if !strings.Contains(body, `"extra":{"region":"eu","nested":{"kwargs":"kept, it is not the bag"}}`) {
		t.Errorf("the rest of extra was not kept as it was: %s", body)
	}
	// Nothing but the bags is still an object, and an empty one.
	onlyBags := render(t, []ListedConnection{{ID: 2, Provider: "vertex_ai", Scope: "user",
		Extra: []byte(`{"kwargs":{"credentials":"fake-file"}}`)}})
	if !strings.Contains(onlyBags, `"extra":{}`) {
		t.Errorf("extra holding only a bag: %s", onlyBags)
	}
}

// The provider list is the one provider there is, whole, so that a field
// added, dropped or changed is seen: the web app draws its form from it.
func TestTheProviderListIsTheOneProvider(t *testing.T) {
	status, body := get(t, served(emptyLister{}, principal.Principal{UserID: testpostgres.Alice, Method: principal.MethodSession}), ProvidersPath)
	want := `[{"provider":"openai_compatible","transport":"OPENAI_COMPATIBLE","discovery":"endpoint","default_base_url":null,` +
		`"base_url_required":true,"auth_style":"bearer","local_only":false,"any_account":true}]`
	if status != http.StatusOK || body != want {
		t.Errorf("the provider list = %d\n got %s\nwant %s", status, body, want)
	}
}

// A body is read as an OpenAI-compatible connection or refused, whichever
// provider it names: there is no other kind (providers.go). Each case is
// what readChange says of the provider and of `extra`.
func TestABodyNamesTheOneProviderOrIsRefused(t *testing.T) {
	for name, test := range map[string]struct {
		body     string
		creating bool
		want     string // a failure's message, or "" for none
	}{
		"the one provider":            {`{"provider":"openai_compatible"}`, true, ""},
		"OpenAI by its own name":      {`{"provider":"openai"}`, true, providerUnknown},
		"Anthropic":                   {`{"provider":"anthropic"}`, true, providerUnknown},
		"Bedrock":                     {`{"provider":"bedrock"}`, true, providerUnknown},
		"Azure":                       {`{"provider":"azure"}`, true, providerUnknown},
		"Vertex AI":                   {`{"provider":"vertex_ai"}`, true, providerUnknown},
		"OpenRouter by its own name":  {`{"provider":"openrouter"}`, true, providerUnknown},
		"Ollama by its own name":      {`{"provider":"ollama_chat"}`, true, providerUnknown},
		"padded":                      {`{"provider":" openai_compatible "}`, true, ""},
		"a change naming no provider": {`{"enabled":false}`, false, ""},
		"a change to no provider":     {`{"provider":null}`, false, providerUnknown},
		"a Bedrock credential bag":    {`{"provider":"openai_compatible","extra":{"litellm_params":{"aws_secret_access_key":"fake-s"}}}`, true, "litellm_params or kwargs"},
		"a Vertex credential bag":     {`{"provider":"openai_compatible","extra":{"kwargs":{}}}`, true, "litellm_params or kwargs"},
		"a bag further down is data":  {`{"provider":"openai_compatible","extra":{"notes":{"kwargs":1}}}`, true, ""},
	} {
		sent, failures := readBody([]byte(test.body))
		if failures == nil {
			_, failures = readChange(sent, test.creating)
		}
		var said []string
		for _, failure := range failures {
			said = append(said, failure.Message)
		}
		got := strings.Join(said, "; ")
		if (test.want == "" && got != "") || (test.want != "" && !strings.Contains(got, test.want)) {
			t.Errorf("%s: failures %q, want %q", name, got, test.want)
		}
	}
}

// An endpoint's error sentence is shown on the settings page, so the key
// and the header values are struck out of it wherever the endpoint echoed
// them, and a long one is cut.
func TestWhatAnEndpointSaidIsShownWithoutTheCredentials(t *testing.T) {
	for name, test := range map[string]struct {
		raw, want string
	}{
		"the key echoed":    {`{"error":{"message":"Incorrect API key provided: fake-key.\n Check it."}}`, "Incorrect API key provided: [key]. Check it."},
		"a header echoed":   {`{"error":{"message":"organisation fake-org is not yours"}}`, "organisation [header] is not yours"},
		"not an error body": {`<html>bad gateway</html>`, ""},
		"a long sentence":   {`{"error":{"message":"` + strings.Repeat("x", 1000) + `"}}`, strings.Repeat("x", maxMessage) + "…"},
	} {
		if got := saidBy([]byte(test.raw), "fake-key", "fake-org"); got != test.want {
			t.Errorf("%s: %q, want %q", name, got, test.want)
		}
	}
}

// A row this package cannot describe truthfully is not described: the list
// fails, and the route turns that into its sanitised 500.
func TestAColumnThatIsNotAnObjectFailsTheList(t *testing.T) {
	if _, err := BuildConnections([]ListedConnection{{ID: 1, Extra: []byte(`["a list"]`)}}); err == nil {
		t.Error("an extra that is a list was rendered")
	}
	rows := []ListedConnection{{ID: 1, Extra: []byte(`{}`)}}
	AttachModels(rows, []ListedModel{{ID: 2, ConnectionID: 1, CapabilitiesOverride: []byte(`7`), Catalog: []byte(`{}`)}})
	if _, err := BuildConnections(rows); err == nil {
		t.Error("a capabilities_override that is a number was rendered")
	}
}

// The provider list is the settings screen's, so a key and an agent are told
// it needs a session; the connection list is the account's own data and is
// answered to any caller the gate let through.
func TestOnlyASessionReadsTheProviders(t *testing.T) {
	for method, want := range map[principal.Method]int{
		principal.MethodSession:             http.StatusOK,
		principal.MethodPersonalAccessToken: http.StatusForbidden,
	} {
		handler := served(emptyLister{}, principal.Principal{UserID: testpostgres.Alice, Method: method})
		if status, body := get(t, handler, ProvidersPath); status != want {
			t.Errorf("%s: providers answered %d %s, want %d", method, status, body, want)
		}
		if status, body := get(t, handler, ConnectionsPath); status != http.StatusOK || body != "[]" {
			t.Errorf("%s: connections answered %d %s, want an empty list", method, status, body)
		}
	}
}

type emptyLister struct{}

func (emptyLister) ConnectionsOfAccount(context.Context, string) ([]ListedConnection, error) {
	return nil, nil
}

func (emptyLister) ModelsOfAccount(context.Context, string) ([]ListedModel, error) {
	return nil, nil
}

// databaseVariable names a throwaway Postgres for this package's database
// test. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the test is skipped.
const databaseVariable = "SOLOCLEF_API_TEST_MODELCONNECTIONS_DATABASE"

// The isolation test, over the real route and a real database: account B can
// see nothing of account A's connections or models, nobody is shown the
// operator's connection, and no stored key leaves the database.
func TestOneAccountNeverSeesAnothersConnections(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO connections (id, provider, api_key, extra, scope, user_id) VALUES
 (1, 'openai',    'fake-alice-key', '{"litellm_params":{"secret":"fake-alice-bag"},"region":"eu"}', 'user', '`+alice+`'),
 (2, 'anthropic', '',               '{}', 'user',   '`+alice+`'),
 (3, 'openai',    'fake-bob-key',   '{}', 'user',   '`+bob+`'),
 (4, 'openai',    'fake-site-key',  '{}', 'global', NULL);
INSERT INTO models (id, connection_id, model_id, display_name) VALUES
 (10, 1, 'alice-model-b', 'Second by id'),
 (11, 3, 'bob-model',     'His'),
 (12, 4, 'site-model',    'The operator''s'),
 (9,  1, 'alice-model-a', 'First by id');`)
	store := Store{Pool: pool}
	asked := func(userID string) string {
		status, body := get(t, served(store, principal.Principal{UserID: userID, Method: principal.MethodSession}), ConnectionsPath)
		if status != http.StatusOK {
			t.Fatalf("%s was answered %d %s", userID, status, body)
		}
		if strings.Contains(body, "fake-") {
			t.Fatalf("a stored credential was answered: %s", body)
		}
		return body
	}

	hers := asked(alice)
	for _, owned := range []string{`"id":1,`, `"id":2,`, `"model_id":"alice-model-a"`, `"model_id":"alice-model-b"`} {
		if !strings.Contains(hers, owned) {
			t.Errorf("her list is missing %s: %s", owned, hers)
		}
	}
	// Whether a key is saved, and never the key: a stored key, then an empty one.
	if strings.Index(hers, `"has_api_key":true`) > strings.Index(hers, `"has_api_key":false`) {
		t.Errorf("has_api_key is not true for the stored key and false for the empty one: %s", hers)
	}
	if strings.Index(hers, "alice-model-a") > strings.Index(hers, "alice-model-b") {
		t.Errorf("a connection's models are not in id order: %s", hers)
	}
	for _, foreign := range []string{"bob-model", "site-model", `"id":3,`, `"id":4,`, `"scope":"global"`} {
		if strings.Contains(hers, foreign) {
			t.Errorf("her list holds %s, which is not hers: %s", foreign, hers)
		}
	}

	his := asked(bob)
	if !strings.Contains(his, `"id":3,`) || !strings.Contains(his, "bob-model") {
		t.Errorf("his own connection is missing: %s", his)
	}
	for _, foreign := range []string{"alice-model", "site-model", `"id":1,`, `"id":2,`, `"id":4,`} {
		if strings.Contains(his, foreign) {
			t.Errorf("his list holds %s, which is not his: %s", foreign, his)
		}
	}

	// An account with no connection of its own is an empty list, not null.
	testpostgres.Exec(t, pool, `DELETE FROM connections WHERE user_id = '`+bob+`'`)
	if body := asked(bob); body != "[]" {
		t.Errorf("an account with no connections was answered %s", body)
	}
}
