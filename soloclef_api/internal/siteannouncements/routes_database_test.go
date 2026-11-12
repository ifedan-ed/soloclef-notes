package siteannouncements

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
const databaseVariable = "SOLOCLEF_API_TEST_SITEANNOUNCEMENTS_DATABASE"

func TestOnlyAnAdministratorsSessionSendsListsAndWithdraws(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	const carol = "33333333-3333-4333-8333-333333333333"
	// Alice administers the site. Carol's account is switched off.
	// site_announcements is not an account's and survives the truncate.
	testpostgres.Exec(t, pool, `
DELETE FROM site_announcements;
UPDATE "user" SET is_superuser = true WHERE id = '`+alice+`';
INSERT INTO "user" (id, email, is_active) VALUES ('`+carol+`', 'carol@example.test', false);`)

	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Pool: pool, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return now }}.Register(api)
	ask := func(userID string, how principal.Method, method, path, body string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: userID, Method: how}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	as := func(userID, method, path, body string) (int, string) {
		t.Helper()
		return ask(userID, principal.MethodSession, method, path, body)
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
	draft := `{"title":"Planned maintenance","body":"Tonight at ten.","link":"/dashboard","expires_at":"2026-11-13T09:00:00Z"}`

	// Everyone but an administrator in a session is refused, on every
	// route, and nothing is written.
	for _, attempt := range []struct{ method, path, body string }{
		{"GET", ListPath, ""},
		{"POST", ListPath, draft},
		{"POST", "/api/v1/admin/announcements/1/withdraw", ""},
	} {
		status, body := as(bob, attempt.method, attempt.path, attempt.body)
		expect("an ordinary account: "+attempt.method+" "+attempt.path, status, body, 403, administratorsOnly)
		for _, how := range []principal.Method{principal.MethodPersonalAccessToken, principal.MethodAgent} {
			status, body = ask(alice, how, attempt.method, attempt.path, attempt.body)
			expect("an administrator's "+string(how)+": "+attempt.method+" "+attempt.path, status, body, 403, administratorsOnly)
		}
	}
	if count("site_announcements") != 0 || count("notifications") != 0 {
		t.Fatal("a refused request wrote something")
	}

	// Sending: one record, and one unread copy for each active account.
	status, body := as(alice, "POST", ListPath, draft)
	expect("her send", status, body, 201,
		`"title":"Planned maintenance"`, `"link":"/dashboard"`, `"created_at":"2026-11-12T09:00:00Z"`,
		`"expires_at":"2026-11-13T09:00:00Z"`, `"withdrawn_at":null`, `"recipient_count":2`, `"read_count":0`, `"status":"live"`)
	if count(`notifications WHERE type = 'site_announcement' AND NOT read AND title = 'Planned maintenance'
		AND message = 'Tonight at ten.' AND metadata ->> 'link' = '/dashboard'
		AND (metadata ->> 'expires_at')::timestamptz = '2026-11-13T09:00:00Z'
		AND metadata ->> 'announcement_id' = (SELECT id::text FROM site_announcements)`) != 2 {
		t.Error("each active account should hold one unread copy naming the announcement, its link and its expiry")
	}
	if count("notifications WHERE user_id = '"+carol+"'") != 0 {
		t.Error("an account that is switched off was sent the announcement")
	}
	if count("site_announcements WHERE created_by_id = '"+alice+"' AND recipient_count = 2") != 1 {
		t.Error("the record does not name its sender and its two recipients")
	}
	status, body = as(alice, "POST", ListPath, `{"title":"","body":"b","link":"javascript:alert(1)"}`)
	expect("a bad draft", status, body, 422, "title", "link")
	if count("site_announcements") != 1 {
		t.Error("a refused draft was stored")
	}

	// Bob reads his copy; the list counts it. He then deletes it, as the
	// inbox route does, and it still counts.
	testpostgres.Exec(t, pool, `UPDATE notifications SET read = true WHERE user_id = '`+bob+`'`)
	status, body = as(alice, "GET", ListPath, "")
	expect("her list", status, body, 200, `{"announcements":[{"id":`, `"recipient_count":2,"read_count":1,"status":"live"`)
	testpostgres.Exec(t, pool, `
UPDATE notifications SET read = true WHERE user_id = '`+alice+`';
DELETE FROM notifications WHERE user_id = '`+bob+`';
UPDATE site_announcements SET cleared_read_count = 1;`)
	status, body = as(alice, "GET", ListPath, "")
	expect("her list after his delete", status, body, 200, `"read_count":2`)

	// Withdrawing takes every copy out, keeps the record and keeps who
	// had read it. Doing it again changes nothing, not even the time.
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM site_announcements`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	status, body = as(alice, "POST", "/api/v1/admin/announcements/"+id+"/withdraw", "")
	expect("her withdrawal", status, body, 200, `"withdrawn_at":"2026-11-12T09:00:00Z"`, `"read_count":2`, `"status":"withdrawn"`)
	if count("notifications") != 0 || count("site_announcements WHERE withdrawn_at IS NOT NULL") != 1 {
		t.Error("the withdrawal left a copy in an inbox or lost the record")
	}
	now = now.Add(time.Hour)
	status, body = as(alice, "POST", "/api/v1/admin/announcements/"+id+"/withdraw", "")
	expect("a second withdrawal", status, body, 200, `"withdrawn_at":"2026-11-12T09:00:00Z"`, `"read_count":2`)
	status, body = as(alice, "POST", "/api/v1/admin/announcements/999999/withdraw", "")
	expect("an announcement nobody sent", status, body, 404, announcementAbsent)
	status, body = as(alice, "POST", "/api/v1/admin/announcements/abc/withdraw", "")
	expect("an id that is not a number", status, body, 422, "announcement_id")

	// An expired one is listed as expired; the newest is first.
	testpostgres.Exec(t, pool, `INSERT INTO site_announcements (title, body, expires_at, created_at)
VALUES ('Old news', 'Gone', '2026-11-01T00:00:00Z', '2026-10-01T00:00:00Z')`)
	status, body = as(alice, "GET", ListPath, "")
	expect("the list with an expired one", status, body, 200, `"title":"Old news"`, `"status":"expired"`)
	if strings.Index(body, "Planned maintenance") > strings.Index(body, "Old news") {
		t.Errorf("the list is not newest first: %s", body)
	}
}
