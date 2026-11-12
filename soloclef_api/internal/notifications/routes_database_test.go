package notifications

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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_NOTIFICATIONS_DATABASE"

func TestAnInboxIsItsOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// site_announcements is not an account's and survives the truncate.
	testpostgres.Exec(t, pool, `
DELETE FROM site_announcements;
INSERT INTO site_announcements (id, title, body, cleared_read_count) VALUES (7, 'Maintenance', 'Tonight', 0);
INSERT INTO notifications (id, user_id, type, title, message, read, metadata, created_at) VALUES
 (1, '`+alice+`', 'document_processing', 'Tides.pdf', 'Indexed', false, '{"status":"completed","document_type":"FILE"}', '2026-11-12T08:00:00Z'),
 (2, '`+alice+`', 'document_processing', 'Moon.pdf', '100% failed', false, '{"status":"failed","document_type":"FILE"}', '2026-11-11T08:00:00Z'),
 (3, '`+alice+`', 'connector_indexing', 'Drive', 'Syncing <now>', true, '{"status":"in_progress","connector_type":"GOOGLE_DRIVE_CONNECTOR"}', '2026-11-10T08:00:00Z'),
 (4, '`+alice+`', 'site_announcement', 'Maintenance', 'Tonight', true, '{"announcement_id":7}', '2026-11-09T08:00:00Z'),
 (5, '`+alice+`', 'site_announcement', 'Old news', 'Gone', false, '{"announcement_id":7,"expires_at":"2026-01-01T00:00:00+00:00"}', '2026-10-01T08:00:00Z'),
 (6, '`+alice+`', 'site_announcement', 'Odd row', 'Kept', false, '{"expires_at":"soon"}', '2026-09-01T08:00:00Z'),
 (7, '`+alice+`', 'connector_deletion', 'Mail', '1000 removed', false, NULL, '2026-09-01T08:00:00Z'),
 (20, '`+bob+`', 'document_processing', 'His.pdf', 'Indexed', false, '{"status":"completed","document_type":"NOTE"}', '2026-11-12T08:00:00Z'),
 (21, '`+bob+`', 'site_announcement', 'Maintenance', 'Tonight', true, '{"announcement_id":7}', '2026-11-09T08:00:00Z');`)

	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Pool: pool, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }}.Register(api)
	call := func(caller principal.Principal, method, path string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		request = request.WithContext(principal.WithPrincipal(request.Context(), caller))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	as := func(userID, method, path string) (int, string) {
		t.Helper()
		return call(principal.Principal{UserID: userID, Method: principal.MethodSession}, method, path)
	}
	count := func(query string) (n int) {
		t.Helper()
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	expect := func(name string, status int, body string, want int, says ...string) {
		t.Helper()
		if status != want {
			t.Errorf("%s: %d %s", name, status, body)
			return
		}
		for _, piece := range says {
			if !strings.Contains(body, piece) {
				t.Errorf("%s: %s lacks %s", name, body, piece)
			}
		}
	}

	// The list: hers, newest first, the expired announcement left out, the
	// malformed expiry and the missing metadata listed all the same.
	status, body := as(alice, "GET", ListPath)
	expect("her inbox", status, body, 200,
		`{"items":[{"id":1,"user_id":"`+alice+`","type":"document_processing","title":"Tides.pdf","message":"Indexed","read":false,"metadata":{"status":"completed","document_type":"FILE"},"created_at":"2026-11-12T08:00:00Z","updated_at":null}`,
		`"message":"Syncing <now>"`, `"id":6`, `{"id":7,`, `"metadata":{},`, `"total":6,"has_more":false,"next_offset":null`)
	if strings.Contains(body, "His.pdf") || strings.Contains(body, "Old news") {
		t.Errorf("her inbox shows his row or an expired announcement: %s", body)
	}
	// Rows 6 and 7 share an instant: the higher id is first, on every page.
	if six, seven := strings.Index(body, `{"id":6,`), strings.Index(body, `{"id":7,`); seven > six {
		t.Errorf("rows of one instant are not ordered by id: %s", body)
	}
	status, body = as(alice, "GET", ListPath+"?limit=2&offset=2")
	expect("her second page", status, body, 200, `"items":[{"id":3,`, `{"id":4,`, `"total":6,"has_more":true,"next_offset":4`)
	status, body = as(alice, "GET", ListPath+"?limit=2&offset=40")
	expect("a page past the end still knows the total", status, body, 200, `"items":[],"total":6,"has_more":false`)
	status, body = as(alice, "GET", ListPath+"?before_date=2026-11-10T08:00:00Z&category=status")
	expect("paging back by date inside a tab", status, body, 200, `"items":[{"id":7,`, `"total":1`)
	status, body = as(alice, "GET", ListPath+"?filter=errors")
	expect("her errors", status, body, 200, `"items":[{"id":2,`, `"total":1`)
	status, body = as(alice, "GET", ListPath+"?search=100%25")
	expect("a search for 100% is not a wildcard", status, body, 200, `"items":[{"id":2,`, `"total":1`)
	status, body = as(alice, "GET", ListPath+"?source_type=doctype:NOTE")
	expect("his document type finds nothing of hers", status, body, 200, `"items":[],"total":0`)
	status, body = as(alice, "GET", ListPath+"?source_type=NOTE")
	expect("an unknown source filter", status, body, 422, "source_type")
	status, body = call(principal.Principal{UserID: alice, Method: principal.MethodPersonalAccessToken}, "GET", ListPath)
	expect("an API key", status, body, 403, sessionRequired)

	// The counts and the source types.
	status, body = as(alice, "GET", UnreadBatchPath)
	expect("her unread per tab", status, body, 200, `{"status":{"total_unread":3,"recent_unread":2},"announcements":{"total_unread":1,"recent_unread":0}}`)
	status, body = as(alice, "GET", UnreadCountPath)
	expect("her unread", status, body, 200, `{"total_unread":4,"recent_unread":2}`)
	status, body = as(alice, "GET", UnreadCountPath+"?category=status&type=document_processing")
	expect("her unread of one type", status, body, 200, `{"total_unread":2,"recent_unread":2}`)
	status, body = as(alice, "GET", UnreadCountPath+"?category=announcements&type=document_processing")
	expect("a type outside its tab", status, body, 200, `{"total_unread":0,"recent_unread":0}`)
	status, body = as(alice, "GET", UnreadCountPath+"?category=comments")
	expect("a tab this version does not have", status, body, 422, "category")
	status, body = as(alice, "GET", SourceTypesPath)
	expect("her source types", status, body, 200,
		`{"sources":[{"key":"connector:GOOGLE_DRIVE_CONNECTOR","type":"GOOGLE_DRIVE_CONNECTOR","category":"connector","count":1},{"key":"document:FILE","type":"FILE","category":"document","count":2}]}`)

	// Marking read.
	status, body = as(bob, "PATCH", "/api/v1/notifications/1/read")
	expect("his mark of hers", status, body, 404, absent)
	status, body = as(alice, "PATCH", "/api/v1/notifications/1/read")
	expect("her mark", status, body, 200, "Notification marked as read")
	status, body = as(alice, "PATCH", "/api/v1/notifications/1/read")
	expect("her second mark", status, body, 200, "already marked as read")
	status, body = as(alice, "PATCH", "/api/v1/notifications/one/read")
	expect("an id that is not a number", status, body, 422, "notification_id")
	if count("notifications WHERE id = 1 AND read AND updated_at = '2026-11-12T09:00:00Z'") != 1 || count("notifications WHERE id = 20 AND NOT read") != 1 {
		t.Error("the mark did not stamp her row, or it reached his")
	}

	// Deleting one: his is absent to her, a running job's is refused, and a
	// read announcement's copy is still counted as read.
	status, body = as(alice, "DELETE", "/api/v1/notifications/20")
	expect("her delete of his", status, body, 404, absent)
	status, body = as(alice, "DELETE", "/api/v1/notifications/3")
	expect("a running job's row", status, body, 409, stillRunning)
	status, body = as(alice, "DELETE", "/api/v1/notifications/4")
	expect("her delete of a read announcement", status, body, 200, "Notification deleted")
	if count("site_announcements WHERE id = 7 AND cleared_read_count = 1") != 1 || count("notifications WHERE id = 21") != 1 {
		t.Error("the announcement lost her read, or his copy went with hers")
	}

	// Mark all, then clear a view, then clear everything.
	status, body = as(alice, "PATCH", ReadAllPath)
	expect("her mark of everything", status, body, 200, `"updated_count":4`)
	if count("notifications WHERE user_id = '"+bob+"' AND NOT read") != 1 {
		t.Error("marking hers read marked his")
	}
	status, body = as(alice, "DELETE", ListPath+"?category=status&filter=errors")
	expect("her clear of one view", status, body, 200, `"deleted_count":1`)
	status, body = as(alice, "DELETE", ListPath+"?filter=nonsense")
	expect("a clear with a filter it does not understand", status, body, 422, "filter")
	status, body = as(alice, "DELETE", ListPath)
	expect("her clear of everything", status, body, 200, `"deleted_count":3`)
	if count("notifications WHERE user_id = '"+alice+"'") != 2 || count("notifications WHERE id IN (3, 5)") != 2 {
		t.Error("a clear should leave only the running job's row and the expired announcement no view lists")
	}
	if count("notifications WHERE user_id = '"+bob+"'") != 2 {
		t.Error("her clear took his rows")
	}
}
