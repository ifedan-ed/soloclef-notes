package notebooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// untouched is a database no statement may reach: a request refused for its
// shape is refused before anything is read.
type untouched struct{ t *testing.T }

func (u untouched) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	u.t.Error("a refused request wrote to the database")
	return pgconn.CommandTag{}, pgx.ErrNoRows
}

func (u untouched) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	u.t.Error("a refused request read the database")
	return nil, pgx.ErrNoRows
}

func (u untouched) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	u.t.Error("a refused request read the database")
	return nothing{}
}

// nothing is a row that was not found.
type nothing struct{}

func (nothing) Scan(...any) error { return pgx.ErrNoRows }

type reply struct {
	status  int
	headers http.Header
	body    string
}

func send(t *testing.T, server *httptest.Server, verb, path, body string) reply {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(verb, server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", testOrigin)
	request.Header.Set("X-Request-ID", "req_test")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return reply{response.StatusCode, response.Header, string(raw)}
}

// TestRefusedBeforeAnythingIsRead: a parameter or a body of the wrong shape
// is a 422 in the app's envelope naming every bad field, a path the router
// should not have matched is its 404, and nothing is read for either.
func TestRefusedBeforeAnythingIsRead(t *testing.T) {
	who := &caller{userID: testpostgres.Alice, method: principal.MethodSession}
	server := httptest.NewServer(served(untouched{t}, who, time.Now))
	defer server.Close()

	cases := []struct {
		verb, path, body string
		status           int
		fields           []string
	}{
		{"GET", "/api/v1/notebooks/def", "", 422, []string{"path.notebook_id"}},
		{"DELETE", "/api/v1/notebooks/search", "", 422, []string{"path.notebook_id"}},
		{"PUT", "/api/v1/notebooks/x/feature", "", 422, []string{"path.notebook_id"}},
		{"POST", "/api/v1/notebooks", `{"title": 1, "icon": 2}`, 422, []string{"body.title", "body.icon"}},
		{"POST", "/api/v1/notebooks", `{"title": "a\u0000"}`, 422, []string{"body.title"}},
		{"POST", "/api/v1/notebooks", `[]`, 422, []string{"body"}},
		{"PUT", "/api/v1/notebook-report-templates/blog_post", `{"title": "x"}`, 422, []string{"body.prompt"}},
		{"PUT", "/api/v1/notebook-report-templates/blog%2Fpost", `{"prompt": "x"}`, 404, nil},
		{"GET", "/api/v1/notebooks/11%2F00", "", 404, nil},
		// A key that is not seeded is a 404 before anything is read.
		{"PUT", "/api/v1/notebook-report-templates/press_release", `{"prompt": "x"}`, 404, nil},
		{"DELETE", "/api/v1/notebook-report-templates/BLOG_POST", "", 404, nil},
		// A whole number wider than any id names no notebook, and is not
		// sent to a statement to find that out.
		{"GET", "/api/v1/notebooks/99999999999999999999999", "", 404, nil},
		{"DELETE", "/api/v1/notebooks/99999999999999999999999", "", 404, nil},
		{"PUT", "/api/v1/notebooks/99999999999999999999999/feature", "", 404, nil},
	}
	for _, c := range cases {
		got := send(t, server, c.verb, c.path, c.body)
		if got.status != c.status {
			t.Errorf("%s %s: status %d, want %d: %s", c.verb, c.path, got.status, c.status, got.body)
			continue
		}
		if c.status != 422 {
			continue
		}
		var envelope struct {
			Error struct {
				Code      string `json:"code"`
				RequestID string `json:"request_id"`
				Fields    []struct {
					Loc []string `json:"loc"`
				} `json:"fields"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(got.body), &envelope); err != nil {
			t.Errorf("%s %s: not the envelope: %s", c.verb, c.path, got.body)
			continue
		}
		fields := []string{}
		for _, field := range envelope.Error.Fields {
			fields = append(fields, strings.Join(field.Loc, "."))
		}
		if strings.Join(fields, ",") != strings.Join(c.fields, ",") || envelope.Error.Code != "VALIDATION_ERROR" ||
			envelope.Error.RequestID != "req_test" || got.headers.Get("Access-Control-Allow-Origin") != testOrigin {
			t.Errorf("%s %s: fields %v (want %v), envelope %s", c.verb, c.path, fields, c.fields, got.body)
		}
	}
}

// A failure nobody expected is the tier's 500, and a browser can read it.
func TestAFailureKeepsItsCORSHeaders(t *testing.T) {
	who := &caller{userID: testpostgres.Alice, method: principal.MethodSession}
	server := httptest.NewServer(served(failing{}, who, time.Now))
	defer server.Close()
	for _, route := range []struct{ verb, path, body string }{
		{"GET", "/api/v1/notebooks", ""},
		{"GET", "/api/v1/notebooks/1100", ""},
		{"POST", "/api/v1/notebooks", `{}`},
		{"DELETE", "/api/v1/notebooks/1100", ""},
		{"PUT", "/api/v1/notebooks/1100/feature", ""},
		{"GET", "/api/v1/notebook-report-templates", ""},
		{"PUT", "/api/v1/notebook-report-templates/blog_post", `{"prompt": "p"}`},
	} {
		got := send(t, server, route.verb, route.path, route.body)
		if got.status != http.StatusInternalServerError || got.headers.Get("Access-Control-Allow-Origin") != testOrigin ||
			!strings.Contains(got.body, `"request_id":"req_test"`) || strings.Contains(got.body, "unreachable") {
			t.Errorf("%s %s: status %d, headers %v: %s", route.verb, route.path, got.status, got.headers, got.body)
		}
	}
}

// failing is a database that is down.
type failing struct{}

var errDown = &pgconn.PgError{Code: "08006", Message: "the database is unreachable"}

func (failing) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errDown
}
func (failing) Query(context.Context, string, ...interface{}) (pgx.Rows, error) { return nil, errDown }
func (failing) QueryRow(context.Context, string, ...interface{}) pgx.Row        { return downRow{} }

type downRow struct{}

func (downRow) Scan(...any) error { return errDown }

// databaseVariable names a throwaway Postgres for this package's database
// test. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the test is skipped.
const databaseVariable = "SOLOCLEF_API_TEST_NOTEBOOKS_DATABASE"

// The seed's two accounts (testdata/notebooks_seed.sql).
const (
	alice = testpostgres.Alice
	bob   = testpostgres.Bob
)

// moment is where the test's clock stands; the seed is dated against it.
var moment = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// TestRoutesOverARealDatabase runs the nine routes over Postgres, the
// seed's rows put back before every case.
//
// It is the isolation test of the package as well. Every route that names a
// notebook or a template is asked, as Bob, about Alice's -- and must answer
// exactly what it answers about one that was never made, and change nothing.
func TestRoutesOverARealDatabase(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	seed, err := os.ReadFile(filepath.Join("testdata", "notebooks_seed.sql"))
	if err != nil {
		t.Fatalf("reading the seed: %v", err)
	}
	who := &caller{}
	server := httptest.NewServer(served(pool, who, func() time.Time { return moment }))
	defer server.Close()

	// count is how many rows a statement finds, for asking what a request
	// left behind.
	count := func(t *testing.T, query string, arguments ...any) (found int) {
		t.Helper()
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+query, arguments...).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}

	const harbour = `{"id":1100,"title":"Harbour study","icon":"📓","created_at":"2026-06-02T08:00:00Z",` +
		`"updated_at":"2026-06-10T09:30:00.250000Z","source_count":3,"enabled_source_count":2,` +
		`"chat_goal":"learning_guide","chat_instructions":"Answer briefly.","response_length":"longer",` +
		`"featured":true,"title_source":"user","title_pending":false}`
	absent := `"message":"` + notebookAbsent + `"`

	cases := []struct {
		name             string
		as               string
		method           principal.Method
		verb, path, body string
		status           int
		// contains must each be in the body, and never must not be.
		contains []string
		never    []string
		// after is asked once the request has been answered.
		after func(t *testing.T)
	}{
		// --- the list ---------------------------------------------------
		{name: "the list is the caller's, most recently used first, by id within a tie",
			as: alice, verb: "GET", path: "/api/v1/notebooks", status: 200,
			contains: []string{`[` + harbour + `,{"id":1102,`, `},{"id":1101,`, `"id":1121`},
			never:    []string{"Bob", `"id":1190`, "workspace"}},
		{name: "the other account's list is its own", as: bob, verb: "GET", path: "/api/v1/notebooks", status: 200,
			contains: []string{`[{"id":1190,"title":"Bob's notebook"`, `"source_count":1`, `"featured":true`},
			never:    []string{"Harbour", `"id":1100`}},
		{name: "an API key reads its account's list", as: alice, method: principal.MethodPersonalAccessToken,
			verb: "GET", path: "/api/v1/notebooks", status: 200, contains: []string{`"id":1100`}},
		{name: "a list survives a naming time nobody can read", as: alice, verb: "GET", path: "/api/v1/notebooks", status: 200,
			contains: []string{`{"id":1121,"title":"A first name"`}},

		// --- one notebook -----------------------------------------------
		{name: "one notebook", as: alice, verb: "GET", path: "/api/v1/notebooks/1100", status: 200, contains: []string{harbour}},
		{name: "a notebook that was never made", as: alice, verb: "GET", path: "/api/v1/notebooks/1199", status: 404, contains: []string{absent}},
		{name: "another account's notebook is the same answer", as: bob, verb: "GET", path: "/api/v1/notebooks/1100", status: 404,
			contains: []string{absent}, never: []string{"Harbour"}},
		{name: "and the other way round", as: alice, verb: "GET", path: "/api/v1/notebooks/1190", status: 404,
			contains: []string{absent}, never: []string{"Bob"}},

		// --- title_pending, one notebook per branch -----------------------
		{name: "pending: a source still indexing", as: alice, verb: "GET", path: "/api/v1/notebooks/1110", status: 200, contains: []string{`"title_source":"auto","title_pending":true`}},
		{name: "not pending: indexing for too long", as: alice, verb: "GET", path: "/api/v1/notebooks/1111", status: 200, contains: []string{`"title_pending":false`}},
		{name: "pending: a source ready a minute ago", as: alice, verb: "GET", path: "/api/v1/notebooks/1112", status: 200, contains: []string{`"title_pending":true`}},
		{name: "not pending: ready three minutes ago", as: alice, verb: "GET", path: "/api/v1/notebooks/1113", status: 200, contains: []string{`"title_pending":false`}},
		{name: "pending: named once, a source ready since", as: alice, verb: "GET", path: "/api/v1/notebooks/1115", status: 200, contains: []string{`"title_pending":true`}},
		{name: "not pending: named after the source was ready", as: alice, verb: "GET", path: "/api/v1/notebooks/1116", status: 200, contains: []string{`"title_pending":false`}},
		{name: "not pending: named too long ago", as: alice, verb: "GET", path: "/api/v1/notebooks/1118", status: 200, contains: []string{`"title_pending":false`}},
		{name: "not pending: named twice", as: alice, verb: "GET", path: "/api/v1/notebooks/1119", status: 200, contains: []string{`"title_pending":false`}},
		{name: "not pending: named by its user", as: alice, verb: "GET", path: "/api/v1/notebooks/1120", status: 200, contains: []string{`"title_source":"user","title_pending":false`}},
		{name: "not pending: a naming time nobody can read", as: alice, verb: "GET", path: "/api/v1/notebooks/1121", status: 200, contains: []string{`"title_pending":false`}},

		// --- create -----------------------------------------------------
		{name: "a notebook is made for its maker", as: alice, verb: "POST", path: "/api/v1/notebooks",
			body: `{"title": "  Tides ", "icon": "🧭"}`, status: 201,
			contains: []string{`{"id":1200,"title":"Tides","icon":"🧭","created_at":"2026-06-15T12:00:00Z","updated_at":"2026-06-15T12:00:00Z","source_count":0,"enabled_source_count":0,"chat_goal":"default","chat_instructions":null,"response_length":"default","featured":false,"title_source":"user","title_pending":false}`},
			after: func(t *testing.T) {
				if count(t, "notebooks WHERE id = 1200 AND user_id = $1 AND icon_source = 'user'", alice) != 1 {
					t.Error("the notebook is not its maker's")
				}
			}},
		{name: "a default title lets the notebook name itself", as: bob, method: principal.MethodAgent, verb: "POST", path: "/api/v1/notebooks",
			body: `{}`, status: 201, contains: []string{`"title":"Untitled notebook"`, `"title_source":"auto"`},
			after: func(t *testing.T) {
				if count(t, "notebooks WHERE id = 1200 AND user_id = $1 AND icon_source = 'auto'", bob) != 1 {
					t.Error("the notebook is not its maker's")
				}
			}},
		// The body cannot say whose the notebook is.
		{name: "a body that names an owner is not obeyed", as: bob, verb: "POST", path: "/api/v1/notebooks",
			body: `{"title": "Mine", "user_id": "` + alice + `", "workspace_id": 7}`, status: 201,
			after: func(t *testing.T) {
				if count(t, "notebooks WHERE title = 'Mine' AND user_id = $1", bob) != 1 {
					t.Error("the notebook was made for somebody the body named")
				}
			}},

		// --- the edit ---------------------------------------------------
		{name: "a rename by its owner names it for good and moves it up the list",
			as: alice, verb: "PATCH", path: "/api/v1/notebooks/1110", body: `{"title": "  Estuaries  "}`, status: 200,
			contains: []string{`"id":1110,"title":"Estuaries"`, `"title_source":"user"`},
			after: func(t *testing.T) {
				if count(t, "notebooks WHERE id = 1110 AND title = 'Estuaries' AND title_source = 'user' AND updated_at = $1", moment) != 1 {
					t.Error("the rename was not written, or did not move updated_at")
				}
			}},
		{name: "only the mentioned fields change",
			as: alice, verb: "PATCH", path: "/api/v1/notebooks/1100",
			body: `{"chat_goal": "custom", "chat_instructions": "   ", "icon": null}`, status: 200,
			contains: []string{`"title":"Harbour study"`, `"icon":null`, `"chat_goal":"custom"`, `"chat_instructions":null`, `"response_length":"longer"`}},
		{name: "a null title is no change", as: alice, verb: "PATCH", path: "/api/v1/notebooks/1100", body: `{"title": null}`, status: 200,
			contains: []string{harbour},
			after: func(t *testing.T) {
				if count(t, "notebooks WHERE id = 1100 AND updated_at = '2026-06-10 09:30:00.25+00'") != 1 {
					t.Error("an edit that changed nothing moved updated_at")
				}
			}},
		{name: "a blank title is refused", as: alice, verb: "PATCH", path: "/api/v1/notebooks/1100", body: `{"title": "  "}`, status: 422,
			contains: []string{"title must not be blank"}},
		{name: "a goal that does not exist is refused", as: alice, verb: "PATCH", path: "/api/v1/notebooks/1100", body: `{"chat_goal": "sing"}`, status: 422,
			contains: []string{"chat_goal"}},
		{name: "another account cannot rename the notebook", as: bob, verb: "PATCH", path: "/api/v1/notebooks/1100", body: `{"title": "Mine now"}`, status: 404,
			contains: []string{absent},
			after: func(t *testing.T) {
				if count(t, "notebooks WHERE id = 1100 AND title = 'Harbour study'") != 1 {
					t.Error("another account's edit renamed the notebook")
				}
			}},

		// --- delete -----------------------------------------------------
		{name: "a delete takes the notebook, its source links and its outputs, and leaves the rest",
			as: alice, verb: "DELETE", path: "/api/v1/notebooks/1100", status: 204,
			after: func(t *testing.T) {
				if count(t, "notebooks WHERE id = 1100") != 0 || count(t, "notebook_sources WHERE notebook_id = 1100") != 0 ||
					count(t, "notebook_outputs WHERE notebook_id = 1100") != 0 {
					t.Error("the notebook, its source links or its outputs are still there")
				}
				// The documents it read from, its chat and its note stay;
				// the last two are no longer filed anywhere.
				if count(t, "documents WHERE id IN (1100, 1101, 1102, 1150)") != 4 ||
					count(t, "new_chat_threads WHERE id = 1100 AND notebook_id IS NULL") != 1 ||
					count(t, "notes WHERE document_id = 1150 AND notebook_id IS NULL") != 1 {
					t.Error("a delete took a document, the chat or the note with it, or left them filed")
				}
			}},
		{name: "a delete of a notebook that was never made", as: alice, verb: "DELETE", path: "/api/v1/notebooks/1199", status: 404, contains: []string{absent}},
		{name: "another account cannot delete the notebook", as: bob, verb: "DELETE", path: "/api/v1/notebooks/1100", status: 404,
			contains: []string{absent},
			after: func(t *testing.T) {
				if count(t, "notebooks WHERE id = 1100") != 1 || count(t, "notebook_sources WHERE notebook_id = 1100") != 3 {
					t.Error("another account's delete removed something")
				}
			}},

		// --- the pin ----------------------------------------------------
		{name: "a pin", as: alice, verb: "PUT", path: "/api/v1/notebooks/1101/feature", status: 200,
			contains: []string{`"id":1101`, `"featured":true`},
			after: func(t *testing.T) {
				// Pinning is not using: the notebook does not rise in the list.
				if count(t, "notebooks WHERE id = 1101 AND featured_at = $1 AND updated_at = '2026-06-09 00:00:00+00'", moment) != 1 {
					t.Error("the pin was not written at the clock's instant, or it moved updated_at")
				}
			}},
		{name: "a second pin keeps the first instant", as: alice, verb: "PUT", path: "/api/v1/notebooks/1100/feature", status: 200,
			contains: []string{harbour},
			after: func(t *testing.T) {
				if count(t, "notebooks WHERE id = 1100 AND featured_at = '2026-06-03 00:00:00+00'") != 1 {
					t.Error("pinning a pinned notebook moved the pin")
				}
			}},
		{name: "an unpin", as: alice, verb: "DELETE", path: "/api/v1/notebooks/1100/feature", status: 200,
			contains: []string{`"id":1100`, `"featured":false`, `"source_count":3`}},
		{name: "an unpin of what was not pinned", as: alice, verb: "DELETE", path: "/api/v1/notebooks/1101/feature", status: 200,
			contains: []string{`"id":1101`, `"featured":false`}},
		{name: "a pin of a notebook that was never made", as: alice, verb: "PUT", path: "/api/v1/notebooks/1199/feature", status: 404, contains: []string{absent}},
		{name: "another account cannot pin the notebook", as: bob, verb: "PUT", path: "/api/v1/notebooks/1101/feature", status: 404,
			contains: []string{absent},
			after: func(t *testing.T) {
				if count(t, "notebooks WHERE id = 1101 AND featured_at IS NULL") != 1 {
					t.Error("another account pinned the notebook")
				}
			}},
		{name: "another account cannot unpin the notebook", as: bob, verb: "DELETE", path: "/api/v1/notebooks/1100/feature", status: 404,
			contains: []string{absent}, never: []string{"Harbour"},
			after: func(t *testing.T) {
				if count(t, "notebooks WHERE id = 1100 AND featured_at IS NOT NULL") != 1 {
					t.Error("another account unpinned the notebook")
				}
			}},

		// --- the report templates ----------------------------------------
		{name: "the templates are the seeds with the caller's own edits", as: alice, verb: "GET", path: "/api/v1/notebook-report-templates", status: 200,
			contains: []string{`[{"key":"learning_overview","format":"interactive","title":"Learning Overview"`,
				`{"key":"study_guide","format":"document","title":"Study Guide","description":"Short-answer quiz, essay questions and a glossary of key terms."`,
				`{"key":"blog_post","format":"document","title":"Alice's post","description":"Hers","prompt":"Write it her way.","customized":true}]`},
			never: []string{"Bob", "his way"}},
		{name: "and each account sees only its own", as: bob, verb: "GET", path: "/api/v1/notebook-report-templates", status: 200,
			contains: []string{`"title":"Bob's guide"`, `"title":"Bob's post"`}, never: []string{"Alice", "her way"}},
		{name: "a first edit of a template", as: alice, verb: "PUT", path: "/api/v1/notebook-report-templates/study_guide",
			body: `{"title": " Her guide ", "prompt": "Guide it her way."}`, status: 200,
			contains: []string{`{"key":"study_guide","format":"document","title":"Her guide","description":"Short-answer quiz, essay questions and a glossary of key terms.","prompt":"Guide it her way.","customized":true}`},
			after: func(t *testing.T) {
				// Bob's edit of the same template is his, untouched.
				if count(t, "notebook_report_templates WHERE user_id = $1 AND template_key = 'study_guide' AND title = 'Bob''s guide'", bob) != 1 ||
					count(t, "notebook_report_templates WHERE user_id = $1 AND template_key = 'study_guide' AND created_at = $2", alice, moment) != 1 {
					t.Error("the edit was not written for its maker alone")
				}
			}},
		{name: "an edit of an edit", as: alice, verb: "PUT", path: "/api/v1/notebook-report-templates/blog_post",
			body: `{"title": "A new post", "description": "", "prompt": "Write it anew."}`, status: 200,
			contains: []string{`"title":"A new post","description":"","prompt":"Write it anew.","customized":true`},
			after: func(t *testing.T) {
				if count(t, "notebook_report_templates WHERE id = 1100 AND updated_at = $1 AND created_at = '2026-06-01 00:00:00+00'", moment) != 1 ||
					count(t, "notebook_report_templates WHERE id = 1190 AND title = 'Bob''s post' AND updated_at = '2026-06-01 00:00:00+00'") != 1 {
					t.Error("the edit was not written over the caller's own row and no other")
				}
			}},
		{name: "a save that changes nothing writes nothing", as: alice, verb: "PUT", path: "/api/v1/notebook-report-templates/blog_post",
			body: `{"title": "Alice's post", "description": "Hers", "prompt": "Write it her way."}`, status: 200,
			after: func(t *testing.T) {
				if count(t, "notebook_report_templates WHERE id = 1100 AND updated_at = '2026-06-01 00:00:00+00'") != 1 {
					t.Error("a save that changed nothing moved updated_at")
				}
			}},
		{name: "a reset discards the caller's edit and nobody else's", as: alice, verb: "DELETE", path: "/api/v1/notebook-report-templates/blog_post", status: 200,
			contains: []string{`{"key":"blog_post","format":"document","title":"Blog Post"`, `"customized":false}]`},
			after: func(t *testing.T) {
				if count(t, "notebook_report_templates WHERE user_id = $1", alice) != 0 || count(t, "notebook_report_templates WHERE user_id = $1", bob) != 2 {
					t.Error("the reset removed the wrong rows")
				}
			}},
		{name: "a reset of a template nobody edited", as: alice, verb: "DELETE", path: "/api/v1/notebook-report-templates/study_guide", status: 200,
			contains: []string{`"title":"Alice's post"`},
			after: func(t *testing.T) {
				if count(t, "notebook_report_templates") != 3 {
					t.Error("a reset of nothing removed something")
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testpostgres.Exec(t, pool, string(seed))
			who.userID, who.method = c.as, c.method
			if who.method == "" {
				who.method = principal.MethodSession
			}
			got := send(t, server, c.verb, c.path, c.body)
			if got.status != c.status {
				t.Fatalf("status %d, want %d: %s", got.status, c.status, got.body)
			}
			for _, part := range c.contains {
				if !strings.Contains(got.body, part) {
					t.Errorf("the answer does not hold %s\n%s", part, got.body)
				}
			}
			for _, part := range c.never {
				if strings.Contains(got.body, part) {
					t.Errorf("the answer holds %s\n%s", part, got.body)
				}
			}
			if c.status == 204 && (got.body != "" || got.headers.Get("Content-Type") != "") {
				t.Errorf("a 204 carried a body or a type: %q, %q", got.body, got.headers.Get("Content-Type"))
			}
			if c.verb == "GET" && c.status == 200 && strings.HasPrefix(c.path, "/api/v1/notebooks") &&
				got.headers.Get("Cache-Control") != privateNoStore {
				t.Errorf("Cache-Control %q", got.headers.Get("Cache-Control"))
			}
			if c.after != nil {
				c.after(t)
			}
		})
	}
}
