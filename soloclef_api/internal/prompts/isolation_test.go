package prompts

import (
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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// test. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the test is skipped.
const databaseVariable = "SOLOCLEF_API_TEST_PROMPTS_DATABASE"

// The isolation test, over the six routes and the real statements. A prompt
// is its account's: another account cannot list, change or remove it. The
// one thing shared is on purpose: a prompt its owner published is on the
// community shelf, where another account may read it and take a copy, and
// the copy is then that account's own and private.
func TestOneAccountNeverSeesOrChangesAnothersPrompts(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
UPDATE "user" SET display_name = 'Alice' WHERE id = '`+alice+`';
INSERT INTO prompts (id, user_id, name, prompt, mode, version, is_public, created_at) VALUES
 (1, '`+alice+`', 'Her private one',   'Summarise {selection}.', 'transform', 3, false, '2026-11-01 10:00:00+00'),
 (2, '`+alice+`', 'Her published one', 'Plan a week.',           'transform', 7, true,  '2026-11-01 10:00:01+00'),
 (3, '`+bob+`',   'His own',           'What else is here?',     'explore',   1, false, '2026-11-01 10:00:02+00');
SELECT setval(pg_get_serial_sequence('prompts', 'id'), 100);`)

	store := Store{Pool: pool}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	at := func() time.Time { return time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC) }
	ReadRoutes{Prompts: store, Errors: envelope, Logger: logger}.Register(api)
	CreateRoute{Pool: pool, Errors: envelope, Logger: logger, Now: at}.Register(api)
	UpdateRoute{Pool: pool, Errors: envelope, Logger: logger}.Register(api)
	DeleteRoute{Pool: pool, Errors: envelope, Logger: logger}.Register(api)
	CopyRoute{Pool: pool, Errors: envelope, Logger: logger, Now: at}.Register(api)

	caller := alice
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		api.Adapter().ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: caller, Method: principal.MethodSession})))
	})
	bodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes("POST "+CreatePath, "PUT "+UpdatePath),
	}
	handler := requestcontext.Middleware(logger, bodies.Wrap(withCaller))
	as := func(userID, method, path, body string) (int, string) {
		t.Helper()
		caller = userID
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}

	// Each library is its owner's alone, newest first, and no prompt carries
	// a workspace.
	status, hers := as(alice, http.MethodGet, ListPath, "")
	if status != http.StatusOK || !strings.Contains(hers, "Her private one") || strings.Contains(hers, "His own") ||
		strings.Contains(hers, "workspace_id") || strings.Index(hers, "Her published one") > strings.Index(hers, "Her private one") {
		t.Fatalf("her library answered %d %s", status, hers)
	}
	// The old filter is not a parameter any more: it changes nothing.
	if _, filtered := as(alice, http.MethodGet, ListPath+"?workspace_id=7", ""); filtered != hers {
		t.Errorf("an old workspace filter changed the answer: %s", filtered)
	}
	status, his := as(bob, http.MethodGet, ListPath, "")
	if status != http.StatusOK || !strings.Contains(his, "His own") || strings.Contains(his, "Her ") {
		t.Fatalf("his library answered %d %s", status, his)
	}

	// The shelf shows him what she published, with her name and not her
	// address, and never what she kept private.
	status, shelf := as(bob, http.MethodGet, PublicPath, "")
	if status != http.StatusOK || !strings.Contains(shelf, "Her published one") || strings.Contains(shelf, "Her private one") ||
		!strings.Contains(shelf, `"author_name":"Alice"`) || strings.Contains(shelf, "alice@example.test") {
		t.Fatalf("the shelf answered %d %s", status, shelf)
	}

	// Her prompts, private and published alike, are not his to change or
	// remove, and he is told what he is told about an id nobody has.
	for _, attempt := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/prompts/1", `{"name":"taken"}`},
		{http.MethodPut, "/api/v1/prompts/2", `{"prompt":"rewritten"}`},
		{http.MethodDelete, "/api/v1/prompts/1", ""},
		{http.MethodDelete, "/api/v1/prompts/2", ""},
		// A private prompt cannot be copied, by its id or any other way.
		{http.MethodPost, "/api/v1/prompts/1/copy", ""},
	} {
		got, said := as(bob, attempt.method, attempt.path, attempt.body)
		nobodys, _ := as(bob, attempt.method, strings.NewReplacer("/1", "/999", "/2", "/999").Replace(attempt.path), attempt.body)
		if got != http.StatusNotFound || nobodys != http.StatusNotFound || !strings.Contains(said, promptAbsent) {
			t.Errorf("%s %s answered %d %s (and %d for an id nobody has)", attempt.method, attempt.path, got, said, nobodys)
		}
	}
	if _, after := as(alice, http.MethodGet, ListPath, ""); after != hers {
		t.Errorf("her library changed\nbefore %s\n after %s", hers, after)
	}

	// His copy of her published prompt is his, private, and starts at 1.
	status, copied := as(bob, http.MethodPost, "/api/v1/prompts/2/copy", "")
	if status != http.StatusOK || !strings.Contains(copied, `"name":"Her published one"`) ||
		!strings.Contains(copied, `"is_public":false`) || !strings.Contains(copied, `"version":1`) {
		t.Fatalf("the copy answered %d %s", status, copied)
	}
	var owner string
	if err := pool.QueryRow(t.Context(), `SELECT user_id::text FROM prompts WHERE id > 100`).Scan(&owner); err != nil || owner != bob {
		t.Errorf("the copy belongs to %q, %v", owner, err)
	}

	// A create is the caller's, and a workspace_id an old client still sends
	// decides nothing. He is no administrator, so he cannot publish.
	status, created := as(bob, http.MethodPost, CreatePath, `{"name":"New","prompt":"Do it.","mode":"transform","workspace_id":7}`)
	if status != http.StatusOK || strings.Contains(created, "workspace_id") {
		t.Errorf("the create answered %d %s", status, created)
	}
	if status, raw := as(bob, http.MethodPost, CreatePath, `{"name":"Loud","prompt":"Do it.","mode":"transform","is_public":true}`); status != http.StatusForbidden || !strings.Contains(raw, publishingRefused) {
		t.Errorf("publishing answered %d %s", status, raw)
	}
	// The flag on the account is the whole of the decision: an address in a
	// setting no longer makes anyone an administrator. With it set he may
	// publish; she, without it, still may not publish hers.
	testpostgres.Exec(t, pool, `UPDATE "user" SET is_superuser = true WHERE id = '`+bob+`'`)
	if status, raw := as(bob, http.MethodPost, CreatePath, `{"name":"Loud","prompt":"Do it.","mode":"transform","is_public":true}`); status != http.StatusOK || !strings.Contains(raw, `"is_public":true`) {
		t.Errorf("an administrator's publishing answered %d %s", status, raw)
	}
	if status, raw := as(alice, http.MethodPut, "/api/v1/prompts/1", `{"is_public":true}`); status != http.StatusForbidden || !strings.Contains(raw, publishingRefused) {
		t.Errorf("her publishing, beside an administrator, answered %d %s", status, raw)
	}
	// The owner changes and removes her own.
	if status, raw := as(alice, http.MethodPut, "/api/v1/prompts/1", `{"name":"Renamed"}`); status != http.StatusOK || !strings.Contains(raw, `"version":4`) {
		t.Errorf("her update answered %d %s", status, raw)
	}
	if status, raw := as(alice, http.MethodDelete, "/api/v1/prompts/1", ""); status != http.StatusOK || raw != `{"success":true}` {
		t.Errorf("her delete answered %d %s", status, raw)
	}
}
