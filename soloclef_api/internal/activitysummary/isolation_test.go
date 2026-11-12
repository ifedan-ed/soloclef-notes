package activitysummary

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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// test. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the test is skipped.
const databaseVariable = "SOLOCLEF_API_TEST_ACTIVITYSUMMARY_DATABASE"

// The isolation test: the summary counts the caller's own log and nothing of
// anyone else's, inside the window the request names.
func TestTheSummaryCountsOnlyTheCallersLog(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO logs (user_id, level, status, message, source, log_metadata, created_at) VALUES
 ('`+alice+`', 'INFO',  'SUCCESS',     'hers, inside',   'indexer', '{"task_name":"index"}', '2026-11-11 08:00:00+00'),
 ('`+alice+`', 'ERROR', 'FAILED',      'hers, failed',   'indexer', NULL,                    '2026-11-11 07:00:00+00'),
 ('`+alice+`', 'INFO',  'SUCCESS',     'hers, too old',  'indexer', NULL,                    '2026-11-09 08:00:00+00'),
 ('`+bob+`',   'ERROR', 'FAILED',      'his failure',    'crawler', NULL,                    '2026-11-11 08:30:00+00'),
 ('`+bob+`',   'INFO',  'IN_PROGRESS', 'his running job','crawler', '{"task_name":"crawl"}', '2026-11-11 08:45:00+00')`)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Route{Logs: Reader{Pool: pool}, Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}, Logger: logger,
		Now: func() time.Time { return time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC) }}.Register(api)
	asked := func(userID, path string) string {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s as %s answered %d %s", path, userID, recorder.Code, recorder.Body.String())
		}
		return recorder.Body.String()
	}

	hers := asked(alice, Path)
	if !strings.Contains(hers, `"total_logs":2`) || !strings.Contains(hers, "hers, failed") {
		t.Errorf("her summary of the last day: %s", hers)
	}
	for _, his := range []string{"his failure", "his running job", "crawler", "crawl"} {
		if strings.Contains(hers, his) {
			t.Errorf("her summary holds %q of his: %s", his, hers)
		}
	}
	// A wider window reaches her older entry, and still none of his.
	if wider := asked(alice, Path+"?hours=72"); !strings.Contains(wider, `"total_logs":3`) || strings.Contains(wider, "his ") {
		t.Errorf("her summary of three days: %s", wider)
	}
	his := asked(bob, Path)
	if !strings.Contains(his, `"total_logs":2`) || strings.Contains(his, "hers") || strings.Contains(his, "indexer") {
		t.Errorf("his summary: %s", his)
	}
}
