package modelchoices

import (
	"context"
	"encoding/json"
	"errors"
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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// proxy is the model proxy's two lists, or the error it is down with.
type proxy struct {
	chat, images []string
	down         error
}

func (p proxy) Names(context.Context) ([]string, error)      { return p.chat, p.down }
func (p proxy) ImageNames(context.Context) ([]string, error) { return p.images, p.down }

// memory is Stored without a database, for the tests of the rules.
type memory map[string]map[Purpose]string

func (m memory) Of(_ context.Context, userID string) (map[Purpose]string, error) {
	return m[userID], nil
}

func (m memory) Choose(_ context.Context, userID string, purpose Purpose, name string) error {
	if m[userID] == nil {
		m[userID] = map[Purpose]string{}
	}
	m[userID][purpose] = name
	return nil
}

func (m memory) Clear(_ context.Context, userID string, purpose Purpose) error {
	delete(m[userID], purpose)
	return nil
}

var offered = proxy{chat: []string{"fast", "site-chat", "smart", "watcher"}, images: []string{"painter", "sketcher"}}

func choices(stored Stored, lists Lists) Choices {
	return Choices{Lists: lists, Stored: stored, ChatDefault: "site-chat", VisionDefault: "watcher"}
}

// served is every route of the package on Huma, behind the body reader the
// tier puts in front of a write, with the caller the gate would have
// resolved in the context.
func served(c Choices, caller principal.Principal) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	PurposeRoute{Path: QuickModelPath, Purpose: Quick, Choices: c, Errors: envelope, Logger: logger}.Register(api)
	PurposeRoute{Path: StudioModelPath, Purpose: Studio, Choices: c, Errors: envelope, Logger: logger}.Register(api)
	RolesRoute{Choices: c, Errors: envelope, Logger: logger}.Register(api)
	SetupRoute{Choices: c, Errors: envelope, Logger: logger}.Register(api)
	bodies := requestbody.Middleware{Covers: func(*http.Request) bool { return true }}
	return bodies.Wrap(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), caller)))
	}))
}

func session(userID string) principal.Principal {
	return principal.Principal{UserID: userID, Method: principal.MethodSession}
}

func call(t *testing.T, handler http.Handler, method, path, body string) (int, string) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// read decodes one job's answer.
func read(t *testing.T, body string) answer {
	t.Helper()
	var got answer
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("the answer %s is not readable: %v", body, err)
	}
	return got
}

func text(value *string) string {
	if value == nil {
		return "<null>"
	}
	return *value
}

// The default of each job, with nothing chosen and then with a chat model
// chosen: quick and studio follow the account's chat model, the picture
// jobs follow the deployment, and a job the deployment names no model for
// has none and is not available.
func TestEachJobsDefault(t *testing.T) {
	stored := memory{}
	c := choices(stored, offered)
	look := func() map[Purpose]Chosen {
		looked, err := c.Look(context.Background(), testpostgres.Alice, Chat, Quick, Studio, Vision, Image)
		if err != nil {
			t.Fatal(err)
		}
		return looked
	}
	for purpose, want := range map[Purpose]Chosen{
		Chat:   {Default: "site-chat", Model: "site-chat", Offered: true},
		Quick:  {Default: "site-chat", Model: "site-chat", Offered: true},
		Studio: {Default: "site-chat", Model: "site-chat", Offered: true},
		Vision: {Default: "watcher", Model: "watcher", Offered: true},
		Image:  {},
	} {
		got := look()[purpose]
		if got.Selected != "" || got.Default != want.Default || got.Model != want.Model || got.Offered != want.Offered {
			t.Errorf("%s with nothing chosen = %+v, want %+v", purpose, got, want)
		}
	}
	if got := look()[Image].Models; strings.Join(got, ",") != "painter,sketcher" {
		t.Errorf("the image job is offered %v, want the proxy's image models", got)
	}

	_ = stored.Choose(context.Background(), testpostgres.Alice, Chat, "smart")
	_ = stored.Choose(context.Background(), testpostgres.Alice, Studio, "fast")
	looked := look()
	if got := looked[Quick]; got.Default != "smart" || got.Model != "smart" {
		t.Errorf("quick does not follow the account's chat model: %+v", got)
	}
	if got := looked[Studio]; got.Default != "smart" || got.Selected != "fast" || got.Model != "fast" {
		t.Errorf("a studio choice does not win over the chat model: %+v", got)
	}
}

// A model withdrawn from the proxy is not replaced by another: the job has
// no model, and the code about to do it is told so.
func TestAWithdrawnModelIsNotReplaced(t *testing.T) {
	stored := memory{}
	c := choices(stored, offered)
	_ = stored.Choose(context.Background(), testpostgres.Alice, Quick, "retired")
	looked, err := c.Look(context.Background(), testpostgres.Alice, Quick)
	if err != nil {
		t.Fatal(err)
	}
	if got := looked[Quick]; got.Offered || got.Model != "retired" {
		t.Errorf("a withdrawn choice = %+v, want it named and not offered", got)
	}
	if name, err := c.ModelFor(context.Background(), testpostgres.Alice, Quick); !errors.Is(err, ErrNotOffered) {
		t.Errorf("ModelFor answered %q, %v for a withdrawn model", name, err)
	}
	if name, err := c.ModelFor(context.Background(), testpostgres.Alice, Studio); err != nil || name != "site-chat" {
		t.Errorf("ModelFor(studio) = %q, %v", name, err)
	}
	// No image model chosen and none named by the deployment: nothing draws.
	if name, err := c.ModelFor(context.Background(), testpostgres.Alice, Image); !errors.Is(err, ErrNotOffered) {
		t.Errorf("ModelFor(image) = %q, %v with no image model anywhere", name, err)
	}
}

// The proxy being down is a 503 that says so, on every route, and never an
// empty list (which would read as "everything was withdrawn").
func TestAProxyThatIsDownIsSaidSo(t *testing.T) {
	handler := served(choices(memory{}, proxy{down: errors.New("connection refused")}), session(testpostgres.Alice))
	for _, path := range []string{QuickModelPath, StudioModelPath, RolesPath, SetupStatusPath} {
		status, body := call(t, handler, http.MethodGet, path, "")
		if status != http.StatusServiceUnavailable || !strings.Contains(body, "cannot be reached") {
			t.Errorf("GET %s with the proxy down = %d %s", path, status, body)
		}
		if strings.Contains(body, "connection refused") {
			t.Errorf("GET %s told the caller the proxy's error: %s", path, body)
		}
	}
	if status, body := call(t, handler, http.MethodPut, QuickModelPath, `{"model":"fast"}`); status != http.StatusServiceUnavailable {
		t.Errorf("choosing with the proxy down = %d %s", status, body)
	}
}

// What a PUT refuses, and that a refusal stores nothing.
func TestWhatChoosingRefuses(t *testing.T) {
	stored := memory{}
	handler := served(choices(stored, offered), session(testpostgres.Alice))
	for name, test := range map[string]struct{ path, body, want string }{
		"no field":            {QuickModelPath, `{}`, "model is required"},
		"not an object":       {QuickModelPath, `[]`, "JSON object"},
		"a number":            {QuickModelPath, `{"model":7}`, "must be a model's name"},
		"blank":               {StudioModelPath, `{"model":"  "}`, "must be a model's name"},
		"not on the proxy":    {StudioModelPath, `{"model":"retired"}`, "is not offered"},
		"an image model":      {QuickModelPath, `{"model":"painter"}`, "is not offered"},
		"roles: nothing":      {RolesPath, `{}`, "Name vision, image or both"},
		"roles: chat as draw": {RolesPath, `{"image":"smart"}`, "is not offered"},
		// One good name and one bad one: neither is stored.
		"roles: half good": {RolesPath, `{"vision":"smart","image":"retired"}`, "is not offered"},
	} {
		status, body := call(t, handler, http.MethodPut, test.path, test.body)
		if status != http.StatusUnprocessableEntity || !strings.Contains(body, test.want) {
			t.Errorf("%s: PUT %s %s = %d %s, want a 422 saying %q", name, test.path, test.body, status, body, test.want)
		}
	}
	if len(stored[testpostgres.Alice]) != 0 {
		t.Errorf("a refused write stored %v", stored[testpostgres.Alice])
	}
}

// Choosing a model is the settings screen's: an API key and an agent may
// read what does their jobs and may not change it, and the setup status
// tells each whether it can.
func TestOnlyASessionChooses(t *testing.T) {
	for method, want := range map[principal.Method]int{
		principal.MethodSession:             http.StatusOK,
		principal.MethodPersonalAccessToken: http.StatusForbidden,
		principal.MethodAgent:               http.StatusForbidden,
	} {
		stored := memory{}
		handler := served(choices(stored, offered), principal.Principal{UserID: testpostgres.Alice, Method: method})
		for path, body := range map[string]string{QuickModelPath: `{"model":"fast"}`, StudioModelPath: `{"model":"fast"}`, RolesPath: `{"image":"painter"}`} {
			if status, answered := call(t, handler, http.MethodPut, path, body); status != want {
				t.Errorf("%s: PUT %s = %d %s, want %d", method, path, status, answered, want)
			}
			if status, answered := call(t, handler, http.MethodGet, path, ""); status != http.StatusOK {
				t.Errorf("%s: GET %s = %d %s", method, path, status, answered)
			}
		}
		if want != http.StatusOK && len(stored[testpostgres.Alice]) != 0 {
			t.Errorf("%s: a refused caller stored %v", method, stored[testpostgres.Alice])
		}
		_, status := call(t, handler, http.MethodGet, SetupStatusPath, "")
		if can := strings.Contains(status, `"can_configure":true`); can != (want == http.StatusOK) {
			t.Errorf("%s: setup status %s", method, status)
		}
	}
}

// The four states of the setup status.
func TestTheSetupStatus(t *testing.T) {
	for name, test := range map[string]struct {
		chosen string
		lists  proxy
		want   string
	}{
		"the deployment's default is offered": {"", offered, `{"status":"ready","source":"global_config","can_configure":true,"stage":"ready"}`},
		"the account's own choice is offered": {"smart", offered, `{"status":"ready","source":"models","can_configure":true,"stage":"ready"}`},
		"its choice was withdrawn":            {"retired", offered, `{"status":"needs_setup","source":"none","can_configure":true,"stage":"recovery"}`},
		"the default was never offered":       {"", proxy{chat: []string{"smart"}}, `{"status":"needs_setup","source":"none","can_configure":true,"stage":"initial_setup"}`},
	} {
		stored := memory{}
		if test.chosen != "" {
			_ = stored.Choose(context.Background(), testpostgres.Alice, Chat, test.chosen)
		}
		status, body := call(t, served(choices(stored, test.lists), session(testpostgres.Alice)), http.MethodGet, SetupStatusPath, "")
		if status != http.StatusOK || body != test.want {
			t.Errorf("%s:\n got %d %s\nwant %s", name, status, body, test.want)
		}
	}
}

// databaseVariable names a throwaway Postgres for this package's database
// test. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the test is skipped.
const databaseVariable = "SOLOCLEF_API_TEST_MODELCHOICES_DATABASE"

// The isolation test, over the real routes and a real database: each
// account chooses, reads back and clears its own models, and nothing one
// account does is seen by, or changes, the other.
func TestOneAccountsChoicesAreNotAnothers(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	c := choices(Rows{Pool: pool}, offered)
	hers, his := served(c, session(alice)), served(c, session(bob))

	put := func(handler http.Handler, path, body string) string {
		t.Helper()
		status, answered := call(t, handler, http.MethodPut, path, body)
		if status != http.StatusOK {
			t.Fatalf("PUT %s %s = %d %s", path, body, status, answered)
		}
		return answered
	}
	get := func(handler http.Handler, path string) string {
		t.Helper()
		status, answered := call(t, handler, http.MethodGet, path, "")
		if status != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, status, answered)
		}
		return answered
	}

	// The whole answer once, so a field added, dropped or renamed is seen.
	if got, want := put(hers, QuickModelPath, `{"model":"fast"}`),
		`{"selected":"fast","model":"fast","available":true,"default":"site-chat",`+
			`"models":[{"id":"fast","label":"fast","own":false},{"id":"site-chat","label":"site-chat","own":false},`+
			`{"id":"smart","label":"smart","own":false},{"id":"watcher","label":"watcher","own":false}]}`; got != want {
		t.Errorf("choosing a quick model answered\n got %s\nwant %s", got, want)
	}
	put(hers, StudioModelPath, `{"model":"smart"}`)
	roles := put(hers, RolesPath, `{"vision":"smart","image":"painter"}`)
	if !strings.Contains(roles, `"vision":{"selected":"smart"`) || !strings.Contains(roles, `"image":{"selected":"painter"`) {
		t.Errorf("her roles after choosing both: %s", roles)
	}

	// Bob sees his own defaults and none of her choices.
	for _, path := range []string{QuickModelPath, StudioModelPath} {
		if got := read(t, get(his, path)); got.Selected != nil || text(got.Model) != "site-chat" {
			t.Errorf("GET %s as Bob after Alice chose: selected %s, model %s", path, text(got.Selected), text(got.Model))
		}
	}
	if got := get(his, RolesPath); strings.Contains(got, `"selected":"`) {
		t.Errorf("Bob's roles carry a choice he did not make: %s", got)
	}

	// Bob choosing and clearing changes only his rows.
	put(his, QuickModelPath, `{"model":"smart"}`)
	put(his, QuickModelPath, `{"model":null}`)
	put(his, RolesPath, `{"image":null,"vision":null}`)
	if got := read(t, get(hers, QuickModelPath)); text(got.Selected) != "fast" {
		t.Errorf("Alice's quick model after Bob cleared his: %s", text(got.Selected))
	}
	if got := get(hers, RolesPath); !strings.Contains(got, `"selected":"painter"`) || !strings.Contains(got, `"selected":"smart"`) {
		t.Errorf("Alice's roles after Bob cleared his: %s", got)
	}

	// A PUT that names one role leaves the other as it was.
	if got := put(hers, RolesPath, `{"image":"sketcher"}`); !strings.Contains(got, `"vision":{"selected":"smart"`) || !strings.Contains(got, `"image":{"selected":"sketcher"`) {
		t.Errorf("changing only the image model: %s", got)
	}
	// Clearing goes back to the default, with no row left to say so.
	if got := read(t, put(hers, StudioModelPath, `{"model":null}`)); got.Selected != nil || text(got.Model) != "site-chat" {
		t.Errorf("her studio model after clearing: selected %s, model %s", text(got.Selected), text(got.Model))
	}
	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM user_models WHERE user_id = $1 OR purpose = 'studio'`, bob).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("rows left for Bob or for a cleared purpose: %d, %v", rows, err)
	}
}

// ownMenus is each account's own models.
type ownMenus map[string][]ownmodels.Option

func (o ownMenus) Menu(_ context.Context, userID string) ([]ownmodels.Option, error) {
	return o[userID], nil
}

// An account's own models join each picker they can serve, marked as its
// own, and another account cannot choose them.
func TestAnAccountsOwnModelsJoinThePickersTheyCanServe(t *testing.T) {
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	c := choices(memory{}, proxy{chat: []string{"site-chat", "own:3"}, images: []string{"painter"}})
	c.Own = ownMenus{alice: {
		{ID: "own:7", RowID: 7, Label: "Her chat", Connection: "models.example.org", Chat: true, Vision: true},
		{ID: "own:8", RowID: 8, Label: "Her painter", Connection: "models.example.org", Images: true},
	}}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	hers, his := served(c, session(alice)), served(c, session(bob))

	status, body := call(t, hers, http.MethodPut, QuickModelPath, `{"model":"own:7"}`)
	// The proxy's "own:3" is left out: it would be read as an own model.
	want := `{"selected":"own:7","model":"own:7","available":true,"default":"site-chat",` +
		`"models":[{"id":"site-chat","label":"site-chat","own":false},` +
		`{"id":"own:7","label":"Her chat","own":true,"connection":"models.example.org"}]}`
	if status != http.StatusOK || body != want {
		t.Fatalf("choosing her own quick model = %d\n got %s\nwant %s", status, body, want)
	}
	status, body = call(t, hers, http.MethodGet, RolesPath, "")
	if status != http.StatusOK || !strings.Contains(body, `"vision":{"selected":null,"model":"watcher","available":false,"default":"watcher",`+
		`"models":[{"id":"site-chat","label":"site-chat","own":false},{"id":"own:7","label":"Her chat","own":true,"connection":"models.example.org"}]}`) {
		t.Fatalf("the vision picker = %d %s", status, body)
	}
	if !strings.Contains(body, `"image":{"selected":null,"model":null,"available":false,"default":null,"models":[{"id":"painter","label":"painter","own":false},{"id":"own:8","label":"Her painter","own":true,"connection":"models.example.org"}]}`) {
		t.Fatalf("the image picker = %s", body)
	}
	// A painter is no chat model, so it is not a studio choice.
	if status, body := call(t, hers, http.MethodPut, StudioModelPath, `{"model":"own:8"}`); status != http.StatusUnprocessableEntity {
		t.Fatalf("her painter as a studio model = %d %s", status, body)
	}
	// Bob has no such model; her id is not his to choose.
	if status, body := call(t, his, http.MethodPut, QuickModelPath, `{"model":"own:7"}`); status != http.StatusUnprocessableEntity || !strings.Contains(body, "is not offered") {
		t.Fatalf("Bob choosing her model = %d %s", status, body)
	}
}
