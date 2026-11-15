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

// The isolation test, over the four routes and the real statements. A prompt
// is its account's: another account cannot list, change or remove it, and
// nothing about a prompt is shared — an old client's is_public is ignored,
// and the routes that served or copied shared prompts are gone.
func TestOneAccountNeverSeesOrChangesAnothersPrompts(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO prompts (id, user_id, name, prompt, mode, version, created_at) VALUES
 (1, '`+alice+`', 'Her first',  'Summarise {selection}.', 'transform', 3, '2026-11-01 10:00:00+00'),
 (2, '`+alice+`', 'Her second', 'Plan a week.',           'transform', 7, '2026-11-01 10:00:01+00'),
 (3, '`+bob+`',   'His own',    'What else is here?',     'explore',   1, '2026-11-01 10:00:02+00');
SELECT setval(pg_get_serial_sequence('prompts', 'id'), 100);`)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	at := func() time.Time { return time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC) }
	ListRoute{Prompts: Store{Pool: pool}, Errors: envelope, Logger: logger}.Register(api)
	CreateRoute{Pool: pool, Errors: envelope, Logger: logger, Now: at}.Register(api)
	UpdateRoute{Pool: pool, Errors: envelope, Logger: logger}.Register(api)
	DeleteRoute{Pool: pool, Errors: envelope, Logger: logger}.Register(api)

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

	// Each library is its owner's alone, newest first; no prompt carries a
	// workspace, and none says it is shared.
	status, hers := as(alice, http.MethodGet, ListPath, "")
	if status != http.StatusOK || !strings.Contains(hers, "Her first") || strings.Contains(hers, "His own") ||
		strings.Contains(hers, "workspace_id") || strings.Contains(hers, "is_public") ||
		strings.Index(hers, "Her second") > strings.Index(hers, "Her first") {
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

	// Her prompts are not his to change or remove, and he is told what he is
	// told about an id nobody has.
	for _, attempt := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/prompts/1", `{"name":"taken"}`},
		{http.MethodPut, "/api/v1/prompts/2", `{"prompt":"rewritten"}`},
		{http.MethodDelete, "/api/v1/prompts/1", ""},
		{http.MethodDelete, "/api/v1/prompts/2", ""},
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

	// The shelf and the copy are gone: neither path runs a route any more.
	for _, gone := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/prompts/public"},
		{http.MethodPost, "/api/v1/prompts/2/copy"},
	} {
		if status, raw := as(bob, gone.method, gone.path, ""); status == http.StatusOK {
			t.Errorf("%s %s still answers %d %s", gone.method, gone.path, status, raw)
		}
	}

	// A create is the caller's. A workspace_id or is_public an old client
	// still sends decides nothing, is not stored, and is not answered.
	status, created := as(bob, http.MethodPost, CreatePath,
		`{"name":"New","prompt":"Do it.","mode":"transform","workspace_id":7,"is_public":true}`)
	if status != http.StatusOK || strings.Contains(created, "workspace_id") || strings.Contains(created, "is_public") ||
		!strings.Contains(created, `"version":1`) {
		t.Errorf("the create answered %d %s", status, created)
	}
	var owner string
	if err := pool.QueryRow(t.Context(), `SELECT user_id::text FROM prompts WHERE id > 100`).Scan(&owner); err != nil || owner != bob {
		t.Errorf("the new prompt belongs to %q, %v", owner, err)
	}

	// An update that names only is_public names nothing a prompt has: the
	// row is answered as it stands and the version is not bumped.
	if status, raw := as(alice, http.MethodPut, "/api/v1/prompts/1", `{"is_public":true}`); status != http.StatusOK ||
		!strings.Contains(raw, `"version":3`) || strings.Contains(raw, "is_public") {
		t.Errorf("an old client's sharing switch answered %d %s", status, raw)
	}
	// The owner changes and removes her own.
	if status, raw := as(alice, http.MethodPut, "/api/v1/prompts/1", `{"name":"Renamed"}`); status != http.StatusOK || !strings.Contains(raw, `"version":4`) {
		t.Errorf("her update answered %d %s", status, raw)
	}
	if status, raw := as(alice, http.MethodDelete, "/api/v1/prompts/1", ""); status != http.StatusOK || raw != `{"success":true}` {
		t.Errorf("her delete answered %d %s", status, raw)
	}
}
