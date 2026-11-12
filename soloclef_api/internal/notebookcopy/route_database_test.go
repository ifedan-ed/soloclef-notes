package notebookcopy

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
const databaseVariable = "SOLOCLEF_API_TEST_NOTEBOOKCOPY_DATABASE"

func TestACopyIsTheOwnersAndCarriesTheSources(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title, icon, chat_goal, chat_instructions, response_length, featured_at, title_source, icon_source) VALUES
 (10, '`+alice+`', 'Tides', '🌊', 'custom', 'Be brief.', 'shorter', now(), 'auto', 'auto'),
 (20, '`+bob+`', 'His', NULL, 'default', NULL, 'default', NULL, 'user', 'user');
INSERT INTO folders (id, user_id, name, position) VALUES (5, '`+alice+`', 'Papers', 'a0');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash) VALUES
 (500, '`+alice+`', 'Moon', 'FILE', '', 'a'), (501, '`+alice+`', 'Sun', 'FILE', '', 'b');
INSERT INTO notebook_sources (notebook_id, user_id, document_id, folder_id, enabled, added_at) VALUES
 (10, '`+alice+`', 500, NULL, true,  '2026-01-01'),
 (10, '`+alice+`', 501, NULL, false, '2026-01-02'),
 (10, '`+alice+`', NULL, 5,   true,  '2026-01-03');
INSERT INTO new_chat_threads (id, user_id, title, notebook_id) VALUES (30, '`+alice+`', 'Chat', 10);
INSERT INTO notebook_public_links (notebook_id, user_id, share_token) VALUES (10, '`+alice+`', 'tok');
SELECT setval(pg_get_serial_sequence('notebooks', 'id'), 100);`)

	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Route{Pool: pool, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }}.Register(api)
	as := func(userID, path, body string) (int, string) {
		t.Helper()
		request := httptest.NewRequest("POST", path, strings.NewReader(body))
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	count := func(query string) (n int) {
		t.Helper()
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The other account cannot copy her notebook, and nothing is written.
	if status, body := as(bob, "/api/v1/notebooks/10/copy", `{"title":"Mine now"}`); status != 404 || !strings.Contains(body, notebookAbsent) {
		t.Errorf("his copy of hers: %d %s", status, body)
	}
	if count("notebooks") != 2 {
		t.Error("a refused copy wrote a notebook")
	}

	status, body := as(alice, "/api/v1/notebooks/10/copy", `{"title":"  Tides, again "}`)
	want := `{"id":101,"title":"Tides, again","icon":"🌊","created_at":"2026-11-12T09:00:00Z","updated_at":"2026-11-12T09:00:00Z",` +
		`"source_count":3,"enabled_source_count":2,"chat_goal":"custom","chat_instructions":"Be brief.","response_length":"shorter",` +
		`"featured":false,"title_source":"user","title_pending":false}`
	if status != 201 || strings.TrimSpace(body) != want {
		t.Errorf("her copy: %d\n got %s\nwant %s", status, body, want)
	}
	// The sources came across with their ticks and dates; the chat, the
	// link and the pin stayed behind.
	if count(`notebook_sources c JOIN notebook_sources o
	          ON o.notebook_id = 10 AND o.document_id IS NOT DISTINCT FROM c.document_id
	         AND o.folder_id IS NOT DISTINCT FROM c.folder_id AND o.enabled = c.enabled AND o.added_at = c.added_at
	          WHERE c.notebook_id = 101 AND c.user_id = '`+alice+`'`) != 3 {
		t.Error("the copy's sources are not the original's")
	}
	if count("new_chat_threads WHERE notebook_id = 101") != 0 || count("notebook_public_links WHERE notebook_id = 101") != 0 ||
		count("notebooks WHERE id = 101 AND featured_at IS NULL AND title_source = 'user' AND icon_source = 'user'") != 1 {
		t.Error("the copy took the chat, the link or the pin, or may rename itself")
	}
	if count("notebook_sources WHERE notebook_id = 10") != 3 || count("notebooks WHERE id = 10 AND title = 'Tides' AND featured_at IS NOT NULL") != 1 {
		t.Error("the original was changed")
	}

	// With no title asked for, it is named after the original; a title too
	// long for the column is cut, by characters.
	for _, body := range []string{``, `{}`, `{"title":null}`, `{"title":"   "}`} {
		if status, answer := as(alice, "/api/v1/notebooks/10/copy", body); status != 201 || !strings.Contains(answer, `"title":"Tides (copy)"`) {
			t.Errorf("a copy asked with %q: %d %s", body, status, answer)
		}
	}
	long := strings.Repeat("海", 250)
	if status, answer := as(alice, "/api/v1/notebooks/10/copy", `{"title":"`+long+`"}`); status != 201 ||
		!strings.Contains(answer, `"title":"`+strings.Repeat("海", 200)+`"`) {
		t.Errorf("a long title: %d %s", status, answer)
	}

	for name, request := range map[string][2]string{
		"a title that is not text":   {"/api/v1/notebooks/10/copy", `{"title":7}`},
		"a body that is not JSON":    {"/api/v1/notebooks/10/copy", `title=x`},
		"an id that is not a number": {"/api/v1/notebooks/ten/copy", `{}`},
	} {
		if status, answer := as(alice, request[0], request[1]); status != 422 {
			t.Errorf("%s: %d %s", name, status, answer)
		}
	}
	if status, answer := as(alice, "/api/v1/notebooks/99999999999999999999999/copy", `{}`); status != 404 {
		t.Errorf("an id no notebook has: %d %s", status, answer)
	}
}
