package agentrules

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// databaseVariable names a throwaway Postgres at this tier's schema (a
// pgvector/pgvector:pg17 brought up to date by soloclef_schema's
// `migrate up`). The test **truncates the tables it seeds**, so it must
// never name a database anyone uses. With it unset the routes are not
// tested, and a skip is not a pass.
//
//	SOLOCLEF_API_TEST_AGENTRULES_DATABASE=postgres://check:check@127.0.0.1:39951/check \
//	  go test ./internal/agentrules/ -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_AGENTRULES_DATABASE"

// The two accounts. Each has a thread of its own (1 is Ada's, 2 is Ben's)
// and a rule of its own (10 is Ada's, 20 is Ben's).
const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
)

const seed = `
TRUNCATE "user" CASCADE;
INSERT INTO "user" (id, email) VALUES ('` + ada + `', 'ada@example.org'), ('` + ben + `', 'ben@example.org');
INSERT INTO new_chat_threads (id, title, user_id) VALUES (1, 'Ada''s', '` + ada + `'), (2, 'Ben''s', '` + ben + `');
INSERT INTO agent_permission_rules (id, user_id, thread_id, permission, pattern, action, created_at) VALUES
  (10, '` + ada + `', NULL, 'web_search', '*', 'allow', '2026-01-01T10:00:00Z'),
  (20, '` + ben + `', 2, 'read_page', 'https://example.org/*', 'deny', '2026-01-02T10:00:00.5Z');
SELECT setval('agent_permission_rules_id_seq', 1000);
`

type world struct {
	t      *testing.T
	pool   *pgxpool.Pool
	server *httptest.Server
}

// newWorld seeds the database and serves the routes over it, with the
// feature switched on unless available says otherwise.
func newWorld(t *testing.T, available Availability) *world {
	t.Helper()
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set: the agent rule routes were NOT tested", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	Routes{
		Store: Store{Pool: pool}, Available: available,
		Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}, Logger: logger,
	}.Register(api)
	// The caller is whoever the tier's gate resolved; here, a header.
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if caller := request.Header.Get("X-Test-Caller"); caller != "" {
			request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: caller, Method: principal.MethodSession}))
		}
		mux.ServeHTTP(response, request)
	})
	bodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes(BodyRoutes()...),
	}
	w := &world{t: t, pool: pool, server: httptest.NewServer(requestcontext.Middleware(logger, bodies.Wrap(withCaller)))}
	t.Cleanup(func() {
		w.server.Close()
		pool.Close()
	})
	return w
}

var on = Availability{PermissionEnabled: true}

type answered struct {
	status int
	raw    string
	body   map[string]any
	list   []any
}

func (a answered) message() string {
	failure, _ := a.body["error"].(map[string]any)
	text, _ := failure["message"].(string)
	return text
}

func (w *world) call(caller, method, path, body string) answered {
	w.t.Helper()
	var sent io.Reader
	if body != "" {
		sent = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, w.server.URL+path, sent)
	if err != nil {
		w.t.Fatal(err)
	}
	if caller != "" {
		request.Header.Set("X-Test-Caller", caller)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := w.server.Client().Do(request)
	if err != nil {
		w.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	got := answered{status: response.StatusCode, raw: string(raw)}
	_ = json.Unmarshal(raw, &got.body)
	_ = json.Unmarshal(raw, &got.list)
	return got
}

// rules is every rule in the table, as "id owner thread permission pattern
// action", so a test can say that nothing moved.
func (w *world) rules() string {
	w.t.Helper()
	var all string
	if err := w.pool.QueryRow(context.Background(), `
SELECT COALESCE(string_agg(concat_ws(' ', id, user_id, COALESCE(thread_id::text, '-'), permission, pattern, action), E'\n' ORDER BY id), '')
FROM agent_permission_rules`).Scan(&all); err != nil {
		w.t.Fatal(err)
	}
	return all
}

func ids(list []any) string {
	out := make([]string, 0, len(list))
	for _, entry := range list {
		object, _ := entry.(map[string]any)
		id, _ := object["id"].(float64)
		out = append(out, strconv.FormatInt(int64(id), 10))
	}
	return strings.Join(out, " ")
}

const path = "/api/v1/agent/permissions/rules"

func TestAnAccountsRulesAreItsOwn(t *testing.T) {
	w := newWorld(t, on)

	// The list is the caller's rules and nobody else's.
	mine := w.call(ada, "GET", path, "")
	if mine.status != 200 || ids(mine.list) != "10" {
		t.Fatalf("Ada's rules: %d %s", mine.status, mine.raw)
	}
	if mine.raw != `[{"id":10,"thread_id":null,"permission":"web_search","pattern":"*","action":"allow","created_at":"2026-01-01T10:00:00Z"}]` {
		t.Errorf("a listed rule: %s", mine.raw)
	}
	if bens := w.call(ben, "GET", path, ""); ids(bens.list) != "20" || !strings.Contains(bens.raw, `"thread_id":2`) || !strings.Contains(bens.raw, `"created_at":"2026-01-02T10:00:00.500000Z"`) {
		t.Errorf("Ben's rules: %s", bens.raw)
	}
	// A query that names another account changes nothing.
	if got := w.call(ben, "GET", path+"?user_id="+ada+"&workspace_id=1", ""); ids(got.list) != "20" {
		t.Errorf("Ben's rules with Ada named in the query: %s", got.raw)
	}

	// A new rule is the caller's, whatever the body says about whose it is.
	made := w.call(ada, "POST", path, `{"permission":" read_page ","action":"ask","user_id":"`+ben+`","workspace_id":7}`)
	if made.status != 201 || made.body["permission"] != "read_page" || made.body["pattern"] != "*" || made.body["action"] != "ask" || made.body["thread_id"] != nil {
		t.Fatalf("create: %d %s", made.status, made.raw)
	}
	if _, says := made.body["user_id"]; says {
		t.Errorf("the rule answers whose it is: %s", made.raw)
	}
	id := int64(made.body["id"].(float64))
	var owner string
	if err := w.pool.QueryRow(context.Background(), `SELECT user_id::text FROM agent_permission_rules WHERE id = $1`, id).Scan(&owner); err != nil || owner != ada {
		t.Errorf("the rule was written for %s, want the caller %s (%v)", owner, ada, err)
	}
	// Scoped to the caller's own thread; never to somebody else's, which
	// is answered exactly as a thread that does not exist.
	if scoped := w.call(ada, "POST", path, `{"permission":"read_page","pattern":"docs/*","action":"deny","thread_id":1}`); scoped.status != 201 || scoped.body["thread_id"] != float64(1) {
		t.Errorf("a rule for Ada's own thread: %d %s", scoped.status, scoped.raw)
	}
	theirs := w.call(ada, "POST", path, `{"permission":"read_page","action":"deny","thread_id":2}`)
	absent := w.call(ada, "POST", path, `{"permission":"read_page","action":"deny","thread_id":404}`)
	wide := w.call(ada, "POST", path, `{"permission":"read_page","action":"deny","thread_id":22000000000000000000}`)
	for name, got := range map[string]answered{"Ben's thread": theirs, "no thread": absent, "a number no thread has": wide} {
		if got.status != 404 || got.message() != threadAbsent {
			t.Errorf("a rule scoped to %s: %d %s", name, got.status, got.raw)
		}
	}

	// A duplicate is refused, an account-wide one included; the same rule
	// is another account's own rule and not a duplicate of this one.
	if again := w.call(ada, "POST", path, `{"permission":"web_search","action":"allow"}`); again.status != 409 || again.message() != duplicateOnCreate {
		t.Errorf("a second identical account-wide rule: %d %s", again.status, again.raw)
	}
	if bens := w.call(ben, "POST", path, `{"permission":"web_search","action":"allow"}`); bens.status != 201 {
		t.Errorf("the same rule for another account: %d %s", bens.status, bens.raw)
	}
	if bad := w.call(ada, "POST", path, `{"permission":"web search!","action":"allow"}`); bad.status != 400 || bad.message() != permissionMalformed {
		t.Errorf("a malformed permission: %d %s", bad.status, bad.raw)
	}
	if bad := w.call(ada, "POST", path, `{"action":"maybe"}`); bad.status != 422 {
		t.Errorf("a body with two faults: %d %s", bad.status, bad.raw)
	}
}

// Another account's rule cannot be read, changed or removed, and each
// attempt is answered exactly as for a rule that was never written.
func TestAnotherAccountsRuleIsNotFound(t *testing.T) {
	w := newWorld(t, on)
	before := w.rules()
	for _, route := range []struct{ method, body string }{
		{"PATCH", `{"action":"allow","pattern":"*"}`},
		{"PATCH", `{}`},
		{"DELETE", ""},
	} {
		theirs := w.call(ada, route.method, path+"/20", route.body)
		never := w.call(ada, route.method, path+"/404", route.body)
		if theirs.status != 404 || theirs.message() != ruleAbsent || never.status != theirs.status || never.message() != theirs.message() {
			t.Errorf("%s %s on Ben's rule: %d %s; on a missing one: %d %s", route.method, route.body, theirs.status, theirs.raw, never.status, never.raw)
		}
	}
	if after := w.rules(); after != before {
		t.Errorf("a refused request changed the rules:\n%s\nwere\n%s", after, before)
	}
	// An id no rule can have is the same answer; one that is not a number
	// is the tier's 422.
	if got := w.call(ada, "DELETE", path+"/99999999999999999999999", ""); got.status != 404 {
		t.Errorf("an id past the column: %d %s", got.status, got.raw)
	}
	if got := w.call(ada, "DELETE", path+"/ten", ""); got.status != 422 {
		t.Errorf("an id that is not a number: %d %s", got.status, got.raw)
	}

	// The owner changes and removes their own.
	changed := w.call(ben, "PATCH", path+"/20", `{"action":"ask","pattern":"   "}`)
	if changed.status != 200 || changed.body["action"] != "ask" || changed.body["pattern"] != "*" || changed.body["thread_id"] != float64(2) {
		t.Errorf("Ben changing his rule: %d %s", changed.status, changed.raw)
	}
	if same := w.call(ben, "PATCH", path+"/20", `{"pattern":null}`); same.status != 200 || same.body["pattern"] != "*" || same.body["action"] != "ask" {
		t.Errorf("an update that mentions nothing: %d %s", same.status, same.raw)
	}
	// A change that would make it the same as another of his is refused.
	if made := w.call(ben, "POST", path, `{"permission":"read_page","pattern":"x","action":"ask","thread_id":2}`); made.status != 201 {
		t.Fatalf("a second rule of Ben's: %d %s", made.status, made.raw)
	} else if clash := w.call(ben, "PATCH", path+"/"+strconv.FormatInt(int64(made.body["id"].(float64)), 10), `{"pattern":"*"}`); clash.status != 409 || clash.message() != duplicateOnUpdate {
		t.Errorf("an update into a duplicate: %d %s", clash.status, clash.raw)
	}
	if gone := w.call(ben, "DELETE", path+"/20", ""); gone.status != 204 || gone.raw != "" {
		t.Errorf("Ben removing his rule: %d %s", gone.status, gone.raw)
	}
	if again := w.call(ben, "DELETE", path+"/20", ""); again.status != 404 {
		t.Errorf("removing it again: %d %s", again.status, again.raw)
	}
	if !strings.HasPrefix(w.rules(), "10 "+ada+" - web_search * allow") {
		t.Errorf("Ada's rule after all of Ben's requests:\n%s", w.rules())
	}
}

// With the feature switched off every route says so, and none reads or
// writes; a request that could never be served is still told why first.
func TestTheRoutesAnswer503WhileTheFeatureIsOff(t *testing.T) {
	for name, available := range map[string]Availability{
		"the flag is off":       {},
		"the kill-switch is on": {Disabled: true, PermissionEnabled: true},
		"both":                  {Disabled: true},
	} {
		w := newWorld(t, available)
		before := w.rules()
		for _, route := range []struct{ method, path, body string }{
			{"GET", path, ""},
			{"POST", path, `{"permission":"p","action":"allow"}`},
			{"PATCH", path + "/10", `{"action":"deny"}`},
			{"DELETE", path + "/10", ""},
		} {
			if got := w.call(ada, route.method, route.path, route.body); got.status != 503 || got.message() != flagOff {
				t.Errorf("%s: %s %s: %d %s", name, route.method, route.path, got.status, got.raw)
			}
		}
		if got := w.call(ada, "POST", path, `{"action":"maybe"}`); got.status != 422 {
			t.Errorf("%s: a body that cannot be read: %d %s", name, got.status, got.raw)
		}
		if after := w.rules(); after != before {
			t.Errorf("%s: a refused request changed the rules", name)
		}
	}
}

// With no caller the wiring is wrong, and that is a 500, never a list.
func TestNoCallerIsAFailure(t *testing.T) {
	w := newWorld(t, on)
	if got := w.call("", "GET", path, ""); got.status != 500 || strings.Contains(got.raw, "web_search") {
		t.Errorf("no caller: %d %s", got.status, got.raw)
	}
}

func TestTheRouteTable(t *testing.T) {
	want := "GET /api/v1/agent/permissions/rules\nPOST /api/v1/agent/permissions/rules\n" +
		"PATCH /api/v1/agent/permissions/rules/{rule_id}\nDELETE /api/v1/agent/permissions/rules/{rule_id}"
	if got := strings.Join(AllRoutes(), "\n"); got != want {
		t.Errorf("routes:\n%s\nwant:\n%s", got, want)
	}
	if got := strings.Join(BodyRoutes(), "\n"); got != "POST /api/v1/agent/permissions/rules\nPATCH /api/v1/agent/permissions/rules/{rule_id}" {
		t.Errorf("routes that read a body:\n%s", got)
	}
}
