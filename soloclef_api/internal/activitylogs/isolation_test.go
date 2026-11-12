package activitylogs

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
const databaseVariable = "SOLOCLEF_API_TEST_ACTIVITYLOGS_DATABASE"

// The isolation test, over the four routes and the real statements: an entry
// goes into the caller's own log, and another account can neither read,
// change nor remove it, and is told exactly what it is told about an entry
// that was never written.
func TestOneAccountNeverSeesOrChangesAnothersLog(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	store := Store{Pool: pool}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	at := time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC)
	ReadRoute{Logs: store, Errors: envelope, Logger: logger}.Register(api)
	CreateRoute{Logs: store, Errors: envelope, Logger: logger, Now: func() time.Time { return at }}.Register(api)
	UpdateRoute{Logs: store, Errors: envelope, Logger: logger}.Register(api)
	DeleteRoute{Logs: store, Errors: envelope, Logger: logger}.Register(api)

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

	// The entry is the caller's. A workspace_id an old client still sends is
	// not a field any more and decides nothing.
	status, made := as(alice, http.MethodPost, CreatePath,
		`{"level":"INFO","status":"IN_PROGRESS","message":"indexing","source":"indexer","log_metadata":{"task_name":"index"},"workspace_id":7}`)
	want := `{"created_at":"2026-11-11T09:30:00Z","id":1,"level":"INFO","status":"IN_PROGRESS","message":"indexing","source":"indexer","log_metadata":{"task_name":"index"}}`
	if status != http.StatusOK || made != want {
		t.Fatalf("the create answered %d\n got %s\nwant %s", status, made, want)
	}
	var owner string
	if err := pool.QueryRow(t.Context(), `SELECT user_id::text FROM logs WHERE id = 1`).Scan(&owner); err != nil || owner != alice {
		t.Fatalf("the entry belongs to %q, %v", owner, err)
	}
	// No metadata is stored as SQL NULL: one way to say "none".
	if status, raw := as(alice, http.MethodPost, CreatePath, `{"level":"ERROR","status":"FAILED","message":"no metadata"}`); status != http.StatusOK || !strings.Contains(raw, `"log_metadata":null`) {
		t.Fatalf("answered %d %s", status, raw)
	}
	var isNull bool
	if err := pool.QueryRow(t.Context(), `SELECT log_metadata IS NULL FROM logs WHERE id = 2`).Scan(&isNull); err != nil || !isNull {
		t.Errorf("no metadata was stored as something other than NULL: %v, %v", isNull, err)
	}

	// Bob: every method, on her entry and on one nobody has, is one answer.
	for _, attempt := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPut, `{"message":"rewritten"}`},
		{http.MethodPut, `{}`},
		{http.MethodDelete, ""},
	} {
		hers, saidHers := as(bob, attempt.method, "/api/v1/logs/1", attempt.body)
		nobodys, saidNobodys := as(bob, attempt.method, "/api/v1/logs/999", attempt.body)
		if hers != http.StatusNotFound || nobodys != http.StatusNotFound || !strings.Contains(saidHers, logAbsent) || !strings.Contains(saidNobodys, logAbsent) {
			t.Errorf("%s %s: %d %s for hers, %d for an id nobody has", attempt.method, attempt.body, hers, saidHers, nobodys)
		}
	}
	if status, raw := as(alice, http.MethodGet, "/api/v1/logs/1", ""); status != http.StatusOK || raw != want {
		t.Fatalf("after his attempts her entry reads %d %s", status, raw)
	}

	// Alice changes and removes her own. A null clears what may be empty and
	// is refused for what may not.
	status, changed := as(alice, http.MethodPut, "/api/v1/logs/1", `{"status":"SUCCESS","source":null}`)
	if status != http.StatusOK || !strings.Contains(changed, `"status":"SUCCESS"`) || !strings.Contains(changed, `"source":null`) || !strings.Contains(changed, `"message":"indexing"`) {
		t.Errorf("the update answered %d %s", status, changed)
	}
	if status, raw := as(alice, http.MethodPut, "/api/v1/logs/1", `{"message":null}`); status != http.StatusUnprocessableEntity {
		t.Errorf("a null message answered %d %s", status, raw)
	}
	if status, raw := as(alice, http.MethodPut, "/api/v1/logs/1", `{}`); status != http.StatusOK || !strings.Contains(raw, `"status":"SUCCESS"`) {
		t.Errorf("an empty update answered %d %s", status, raw)
	}
	if status, raw := as(alice, http.MethodDelete, "/api/v1/logs/1", ""); status != http.StatusOK || !strings.Contains(raw, deletedMessage) {
		t.Errorf("the delete answered %d %s", status, raw)
	}
	if status, _ := as(alice, http.MethodDelete, "/api/v1/logs/1", ""); status != http.StatusNotFound {
		t.Errorf("deleting it again answered %d", status)
	}
}
