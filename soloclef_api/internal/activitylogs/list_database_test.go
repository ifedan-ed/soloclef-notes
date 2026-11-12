package activitylogs

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// The list, over the real statement: an account is listed its own entries
// and no others, newest first, filtered and paged.
func TestTheLogListIsTheCallersOwnNewestFirst(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO logs (id, user_id, level, status, message, source, log_metadata, created_at) VALUES
 (1, '`+alice+`', 'INFO',  'SUCCESS',     'indexed a file',  'nextcloud_import', '{"task_name":"index"}', '2026-11-10T08:00:00Z'),
 (2, '`+alice+`', 'ERROR', 'FAILED',      'could not parse', 'upload',           NULL,                    '2026-11-11T09:00:00Z'),
 (3, '`+alice+`', 'INFO',  'IN_PROGRESS', 'indexing',        'nextcloudximport', '[1,2]',                 '2026-11-11T09:00:00Z'),
 (4, '`+alice+`', 'INFO',  'SUCCESS',     'drawn',           NULL,               '{"n":1}',               '2026-11-12T10:00:00Z'),
 (5, '`+bob+`',   'ERROR', 'FAILED',      'his failure',     'upload',           NULL,                    '2026-11-12T11:00:00Z');`)

	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	ListRoute{Logs: Store{Pool: pool}, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	ids := regexp.MustCompile(`"id":(\d+)`)
	listed := func(userID, query string) (int, string, string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, ListPath+query, nil)
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		var order []string
		for _, match := range ids.FindAllStringSubmatch(recorder.Body.String(), -1) {
			order = append(order, match[1])
		}
		return recorder.Code, strings.Join(order, ","), recorder.Body.String()
	}

	for _, c := range []struct{ name, user, query, want string }{
		// Entries 2 and 3 share an instant; the id decides, every time.
		{"all of hers, newest first", alice, "", "4,3,2,1"},
		{"his are his alone", bob, "", "5"},
		{"by level", alice, "?level=ERROR", "2"},
		{"by status", alice, "?status=SUCCESS", "4,1"},
		// An underscore is a character here, not ILIKE's "any one".
		{"by source, as text", alice, "?source=nextcloud_IMPORT", "1"},
		{"by source, a part of it", alice, "?source=nextcloud", "3,1"},
		{"a percent sign matches nothing of hers", alice, "?source=%25", ""},
		{"from a day", alice, "?start_date=2026-11-11", "4,3,2"},
		{"until a day, the day included", alice, "?end_date=2026-11-11", "3,2,1"},
		{"a page", alice, "?limit=2", "4,3"},
		{"the next page", alice, "?limit=2&skip=2", "2,1"},
		{"his level filter never reaches hers", bob, "?level=INFO", ""},
	} {
		status, order, body := listed(c.user, c.query)
		if status != http.StatusOK || order != c.want {
			t.Errorf("%s: %d, entries %q, want %q\n%s", c.name, status, order, c.want, body)
		}
	}

	// The shape the page reads, and the one odd row: entry 3's metadata is
	// a list, which no entry should have. It is listed without it, and the
	// entries around it are still there.
	_, _, body := listed(alice, "")
	for _, piece := range []string{
		`{"created_at":"2026-11-12T10:00:00Z","id":4,"level":"INFO","status":"SUCCESS","message":"drawn","source":null,"log_metadata":{"n":1}}`,
		`"id":3,"level":"INFO","status":"IN_PROGRESS","message":"indexing","source":"nextcloudximport","log_metadata":null`,
	} {
		if !strings.Contains(body, piece) {
			t.Errorf("the list lacks %s\n%s", piece, body)
		}
	}
	if status, _, body := listed(alice, "?level=LOUD"); status != http.StatusUnprocessableEntity || !strings.Contains(body, "level") {
		t.Errorf("an unknown level answered %d %s", status, body)
	}
	if _, _, body := listed(bob, "?limit=1"); !strings.HasPrefix(body, "[") {
		t.Errorf("the list is not an array: %s", body)
	}
}
