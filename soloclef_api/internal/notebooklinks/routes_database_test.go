package notebooklinks

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_NOTEBOOKLINKS_DATABASE"

// world is the routes over a real Postgres and a real local file store
// behind a real listener: a stream's write deadline is the connection's,
// and a recorder has none.
type world struct {
	t      *testing.T
	pool   *pgxpool.Pool
	server *httptest.Server
	clock  *time.Time
}

// as sends a request. A userID of "" is a visitor: no caller at all.
func (w world) as(userID, method, path, body string, headers ...string) (*http.Response, string) {
	w.t.Helper()
	request, err := http.NewRequest(method, w.server.URL+path, strings.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	if userID != "" {
		request.Header.Set("X-Test-User", userID)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response, err := w.server.Client().Do(request)
	if err != nil {
		w.t.Fatal(err)
	}
	defer response.Body.Close()
	answer, _ := io.ReadAll(response.Body)
	return response, string(answer)
}

func (w world) expect(name string, response *http.Response, body string, want int, says ...string) {
	w.t.Helper()
	if response.StatusCode != want {
		w.t.Errorf("%s: %d %s", name, response.StatusCode, body)
		return
	}
	for _, piece := range says {
		if !strings.Contains(body, piece) {
			w.t.Errorf("%s: %s lacks %s", name, body, piece)
		}
	}
}

func open(t *testing.T, files map[string]string) (world, func(query string) int) {
	pool := testpostgres.Open(t, databaseVariable)
	directory := t.TempDir()
	for key, content := range files {
		path := filepath.Join(directory, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	local, err := filestore.OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	stores, err := filestore.NewStores(local)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	routes := Routes{Pool: pool, Files: stores, StallTimeout: 5 * time.Second, FrontendURL: "https://notes.example.test/",
		Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return clock }}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		// Stands in for the gate: a caller only when the test names one.
		if userID := request.Header.Get("X-Test-User"); userID != "" {
			request = request.WithContext(principal.WithPrincipal(request.Context(),
				principal.Principal{UserID: userID, Method: principal.MethodSession}))
		}
		api.Adapter().ServeHTTP(response, request)
	}))
	t.Cleanup(server.Close)
	count := func(query string) (n int) {
		t.Helper()
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	testpostgres.Exec(t, pool, seed)
	return world{t: t, pool: pool, server: server, clock: &clock}, count
}

const alice, bob = testpostgres.Alice, testpostgres.Bob

// seed is Alice's notebook 10 with three kinds of source, a chat, finished
// and unfinished outputs; a second notebook of hers that is not shared; and
// Bob's notebook. Everything Alice owns outside notebook 10 is something a
// visitor to notebook 10's link must not reach.
const seed = `
INSERT INTO notebooks (id, user_id, title, icon, chat_instructions) VALUES
 (10, '` + alice + `', 'Tides', '🌊', 'Owner-only instructions'),
 (11, '` + alice + `', 'Private', NULL, NULL),
 (20, '` + bob + `', 'His', NULL, NULL);
INSERT INTO folders (id, user_id, name, position) VALUES (5, '` + alice + `', 'Papers', 'a0');
INSERT INTO documents (id, user_id, title, document_type, content, source_markdown, content_hash, folder_id, status) VALUES
 (500, '` + alice + `', 'Moon.pdf', 'FILE', 'moon text', NULL, 'h500', NULL, '{"state":"ready"}'),
 (501, '` + alice + `', 'Sun notes', 'NOTE', 'sun content', '# Sun', 'h501', NULL, '{"state":"ready"}'),
 (502, '` + alice + `', 'In folder', 'FILE', 'folder text', NULL, 'h502', 5, '{"state":"ready"}'),
 (503, '` + alice + `', 'Going', 'FILE', 'going', NULL, 'h503', NULL, '{"state":"deleting"}'),
 (504, '` + alice + `', 'Diary', 'NOTE', 'private diary', NULL, 'h504', NULL, '{"state":"ready"}'),
 (600, '` + alice + `', 'Tide report', 'ARTIFACT', 'report text', '# Report', 'h600', NULL, '{"state":"ready"}'),
 (601, '` + alice + `', 'Tide film', 'ARTIFACT', '', NULL, 'h601', NULL, '{"state":"ready"}'),
 (602, '` + alice + `', 'Chat table', 'ARTIFACT', 'table', NULL, 'h602', NULL, '{"state":"ready"}'),
 (603, '` + alice + `', 'Private deck', 'ARTIFACT', 'private deck', NULL, 'h603', NULL, '{"state":"ready"}'),
 (604, '` + alice + `', 'Draft', 'ARTIFACT', 'draft', NULL, 'h604', NULL, '{"state":"ready"}'),
 (700, '` + bob + `', 'His paper', 'FILE', 'his text', NULL, 'h700', NULL, '{"state":"ready"}');
INSERT INTO document_files (id, document_id, user_id, kind, storage_backend, storage_key, original_filename, mime_type, size_bytes, checksum_sha256, created_at) VALUES
 (90, 500, '` + alice + `', 'ORIGINAL', 'local', 'documents/500/moon.pdf', 'Moon.pdf', 'application/octet-stream', 12, 'c90', '2026-01-01'),
 (91, 500, '` + alice + `', 'REDACTED', 'local', 'documents/500/redacted.pdf', 'redacted.pdf', 'application/pdf', 8, 'c91', '2026-01-02'),
 (92, 504, '` + alice + `', 'ORIGINAL', 'local', 'documents/504/diary.txt', 'diary.txt', 'text/plain', 7, 'c92', '2026-01-01'),
 (93, 502, '` + alice + `', 'ORIGINAL', 'local', 'documents/502/page.html', 'page.html', 'text/html', 9, 'c93', '2026-01-01');
INSERT INTO chunks (id, document_id, user_id, content, page_start) VALUES
 (41, 500, '` + alice + `', 'moon pulls', 3), (42, 504, '` + alice + `', 'dear diary', 1);
INSERT INTO notebook_sources (notebook_id, user_id, document_id, folder_id, enabled, added_at) VALUES
 (10, '` + alice + `', 501, NULL, true,  '2026-01-02'),
 (10, '` + alice + `', 500, NULL, false, '2026-01-01'),
 (10, '` + alice + `', 503, NULL, true,  '2026-01-03'),
 (10, '` + alice + `', 600, NULL, true,  '2026-01-04'),
 (10, '` + alice + `', NULL, 5,   true,  '2026-01-05'),
 (11, '` + alice + `', 504, NULL, true,  '2026-01-01');
INSERT INTO artifacts (id, document_id, user_id, format, generation, metadata, updated_at) VALUES
 (70, 600, '` + alice + `', 'report', 2, NULL, '2026-02-01'),
 (71, 601, '` + alice + `', 'video', 1, '{"duration_seconds": 61.6}', '2026-02-01'),
 (72, 602, '` + alice + `', 'markdown', 1, NULL, '2026-02-01'),
 (73, 603, '` + alice + `', 'slides', 1, NULL, '2026-02-01'),
 (74, 604, '` + alice + `', 'report', 1, NULL, '2026-02-01');
INSERT INTO artifact_files (id, artifact_id, role, storage_backend, storage_key, original_filename, mime_type, size_bytes, checksum_sha256) VALUES
 (80, 71, 'preview', 'local', 'artifacts/71/poster.png', 'poster.png', 'image/png', 6, 'c80'),
 (81, 71, 'primary', 'local', 'artifacts/71/film.mp4', 'render-8f2c.mp4', 'video/mp4', 10, 'c81'),
 (82, 73, 'primary', 'local', 'artifacts/73/deck.html', 'deck.html', 'text/html', 12, 'c82');
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, artifact_id) VALUES
 (180, '` + alice + `', 'report', 'Tide report', 't1', 'ready', 70),
 (181, '` + alice + `', 'video', 'Tide film', 't2', 'ready', 71),
 (182, '` + alice + `', 'report', 'Draft', 't3', 'running', 74),
 (183, '` + alice + `', 'slides', 'Private deck', 't4', 'ready', 73);
INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title, deliverable_job_id, created_at) VALUES
 (1001, 10, '` + alice + `', 'report', 'Tide report', 180, '2026-03-01'),
 (1002, 10, '` + alice + `', 'video', 'Tide film', 181, '2026-03-02'),
 (1003, 10, '` + alice + `', 'report', 'Draft', 182, '2026-03-03'),
 (1004, 11, '` + alice + `', 'slides', 'Private deck', 183, '2026-03-04');
INSERT INTO new_chat_threads (id, user_id, title, notebook_id, archived, source, updated_at) VALUES
 (30, '` + alice + `', 'Older', 10, false, 'surfsense', '2026-04-01'),
 (31, '` + alice + `', 'Newest', 10, false, 'surfsense', '2026-04-02'),
 (32, '` + alice + `', 'Archived', 10, true, 'surfsense', '2026-04-03'),
 (33, '` + alice + `', 'Studio', 10, false, 'artifact_api', '2026-04-04'),
 (34, '` + alice + `', 'Elsewhere', 11, false, 'surfsense', '2026-04-05');
INSERT INTO new_chat_messages (thread_id, role, content, created_at) VALUES
 (30, 'USER', '"an older chat"', '2026-04-01'),
 (31, 'USER', '[{"type":"text","text":"What moves the tides?"}]', '2026-04-02 10:00'),
 (31, 'ASSISTANT', '[{"type":"tool-call","toolName":"search_documents","toolCallId":"c1","args":{"query":"hidden query"},"result":{}},
   {"type":"text","text":"The moon [citation:41], not the diary [citation:42]."},
   {"type":"tool-call","toolName":"save_artifact","toolCallId":"c2","args":{},"result":{"status":"saved","artifact_id":72}},
   {"type":"tool-call","toolName":"save_artifact","toolCallId":"c3","args":{},"result":{"status":"saved","artifact_id":73}}]', '2026-04-02 10:01'),
 (32, 'USER', '"an archived chat"', '2026-04-03'),
 (33, 'USER', '"a studio prompt"', '2026-04-04'),
 (34, 'USER', '"another notebook"', '2026-04-05');
`

var tokenShape = regexp.MustCompile(`"share_token":"([A-Za-z0-9_-]{64})"`)

func TestALinkIsItsOwnersToMakeAndStop(t *testing.T) {
	w, count := open(t, nil)

	response, body := w.as(alice, "GET", "/api/v1/notebooks/10/public-link", "")
	w.expect("no link yet", response, body, 200, `{"link":null}`)
	response, body = w.as(bob, "GET", "/api/v1/notebooks/10/public-link", "")
	w.expect("her notebook asked by him", response, body, 404, notebookAbsent)
	response, body = w.as(bob, "POST", "/api/v1/notebooks/10/public-link", `{}`)
	w.expect("his share of her notebook", response, body, 404, notebookAbsent)
	if count("notebook_public_links") != 0 {
		t.Fatal("a refused share wrote a link")
	}

	// No body: seven days.
	response, body = w.as(alice, "POST", "/api/v1/notebooks/10/public-link", "")
	w.expect("her share", response, body, 200, `"notebook_id":10`, `"notebook_title":"Tides"`, `"notebook_icon":"🌊"`,
		`"created_at":"2026-11-12T09:00:00Z"`, `"expires_at":"2026-11-19T09:00:00Z"`, `"is_expired":false`)
	found := tokenShape.FindStringSubmatch(body)
	if found == nil {
		t.Fatalf("no 64-character token in %s", body)
	}
	first := found[1]
	if !strings.Contains(body, `"public_url":"https://notes.example.test/public/notebook/`+first+`"`) {
		t.Errorf("the public URL is not the web app's address and the token: %s", body)
	}
	for _, leaked := range []string{alice, "alice@example.test", "created_by", "can_revoke"} {
		if strings.Contains(body, leaked) {
			t.Errorf("a link's answer holds %q: %s", leaked, body)
		}
	}

	// Sharing again keeps the address and re-dates the expiry.
	response, body = w.as(alice, "POST", "/api/v1/notebooks/10/public-link", `{"expires_in_days":30}`)
	w.expect("a new expiry", response, body, 200, `"share_token":"`+first+`"`, `"expires_at":"2026-12-12T09:00:00Z"`)
	response, body = w.as(alice, "POST", "/api/v1/notebooks/10/public-link", `{"expires_in_days":null}`)
	w.expect("no expiry", response, body, 200, `"share_token":"`+first+`"`, `"expires_at":null`)
	for name, bad := range map[string]string{
		"days that are not offered": `{"expires_in_days":8}`, "days that are text": `{"expires_in_days":"7"}`,
		"a flag that is not one": `{"regenerate":"yes"}`, "a body that is not an object": `[7]`,
	} {
		response, body = w.as(alice, "POST", "/api/v1/notebooks/10/public-link", bad)
		w.expect(name, response, body, 422)
	}

	// Regenerating gives a new address; the old one stops at once.
	response, body = w.as(alice, "POST", "/api/v1/notebooks/10/public-link", `{"regenerate":true}`)
	w.expect("a new address", response, body, 200)
	second := tokenShape.FindStringSubmatch(body)[1]
	if second == first {
		t.Error("regenerate kept the address")
	}
	response, body = w.as("", "GET", "/api/v1/public-notebooks/"+first, "")
	w.expect("the old address", response, body, 404, gone)
	response, body = w.as("", "GET", "/api/v1/public-notebooks/"+second, "")
	w.expect("the new address", response, body, 200, `"title":"Tides"`)

	// An expired link is refused, is listed as expired, and sharing again
	// gives it a new address: the dead one must not come back to life.
	*w.clock = w.clock.AddDate(0, 0, 8)
	response, body = w.as("", "GET", "/api/v1/public-notebooks/"+second, "")
	w.expect("the address after its time", response, body, 410, expired)
	response, body = w.as(alice, "GET", "/api/v1/notebooks/10/public-link", "")
	w.expect("her expired link", response, body, 200, `"is_expired":true`, second)
	response, body = w.as(alice, "POST", "/api/v1/notebooks/10/public-link", `{"expires_in_days":7}`)
	w.expect("sharing an expired link again", response, body, 200, `"is_expired":false`, `"created_at":"2026-11-20T09:00:00Z"`)
	third := tokenShape.FindStringSubmatch(body)[1]
	if third == second {
		t.Error("an expired address was revived")
	}

	// The list is hers alone; he can stop neither her link nor see it.
	response, body = w.as(alice, "POST", "/api/v1/notebooks/11/public-link", `{}`)
	w.expect("her second link", response, body, 200)
	response, body = w.as(bob, "POST", "/api/v1/notebooks/20/public-link", `{}`)
	w.expect("his own link", response, body, 200, `"notebook_title":"His"`)
	response, body = w.as(alice, "GET", "/api/v1/notebook-links", "")
	w.expect("her links", response, body, 200, `"notebook_title":"Tides"`, `"notebook_title":"Private"`)
	if strings.Contains(body, "His") || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("her list shows his link or may be cached: %s", body)
	}
	// Her first link is row 1: sharing again updated it and never replaced it.
	response, body = w.as(bob, "DELETE", "/api/v1/notebooks/10/public-link", "")
	w.expect("his stop of her link", response, body, 404, linkAbsent)
	response, body = w.as(bob, "DELETE", "/api/v1/notebook-links/1", "")
	w.expect("his revoke of her link by id", response, body, 404, linkAbsent)
	if count("notebook_public_links WHERE user_id = '"+alice+"'") != 2 {
		t.Fatal("he stopped one of her links")
	}
	response, body = w.as(alice, "DELETE", "/api/v1/notebook-links/1", "")
	w.expect("her revoke by id", response, body, 204)
	response, body = w.as("", "GET", "/api/v1/public-notebooks/"+third, "")
	w.expect("a revoked address", response, body, 404, gone)
	response, body = w.as(alice, "DELETE", "/api/v1/notebooks/11/public-link", "")
	w.expect("her stop", response, body, 204)
	response, body = w.as(alice, "DELETE", "/api/v1/notebooks/11/public-link", "")
	w.expect("a second stop", response, body, 404, linkAbsent)
	response, body = w.as(alice, "DELETE", "/api/v1/notebook-links/x", "")
	w.expect("an id that is not a number", response, body, 422)
	if count("notebook_public_links") != 1 {
		t.Error("his link did not survive her stops")
	}
}

func TestAVisitorReachesTheSharedNotebookAndNothingElse(t *testing.T) {
	w, _ := open(t, map[string]string{
		"documents/500/moon.pdf":     "%PDF-1.7 moo",
		"documents/500/redacted.pdf": "redacted",
		"documents/504/diary.txt":    "private",
		"documents/502/page.html":    "<script>x",
		"artifacts/71/film.mp4":      "0123456789",
		"artifacts/71/poster.png":    "poster",
		"artifacts/73/deck.html":     "private deck",
	})
	testpostgres.Exec(t, w.pool, `
INSERT INTO notebook_public_links (id, notebook_id, user_id, share_token, expires_at) VALUES
 (1, 10, '`+alice+`', 'tides-token', '2026-12-01'),
 (2, 20, '`+bob+`', 'his-token', NULL);`)
	const base = "/api/v1/public-notebooks/tides-token"

	// A signed-in stranger is a visitor like any other: the same answer.
	for _, who := range []string{"", bob} {
		response, body := w.as(who, "GET", base, "")
		want := `{"notebook":{"title":"Tides","icon":"🌊"},"expires_at":"2026-12-01T00:00:00Z",` +
			`"messages":[{"role":"user","content":[{"text":"What moves the tides?","type":"text"}],"created_at":"2026-04-02T10:00:00Z"},` +
			`{"role":"assistant","content":[{"text":"The moon [citation:1], not the diary.","type":"text"},` +
			`{"args":{},"result":{"artifact_id":72,"format":"markdown","title":"Chat table"},"toolCallId":"artifact-1-1","toolName":"notebook_artifact","type":"tool-call"},` +
			`{"args":{},"result":{"artifact_id":73,"format":"slides","title":"Private deck"},"toolCallId":"artifact-1-2","toolName":"notebook_artifact","type":"tool-call"}],` +
			`"created_at":"2026-04-02T10:01:00Z"}],` +
			`"citations":{"1":{"source_id":500,"title":"Moon.pdf","page":3}},` +
			`"outputs":[{"id":1002,"kind":"video","title":"Tide film","format":"video","artifact_id":71,"duration_seconds":62,"created_at":"2026-03-02T00:00:00Z"},` +
			`{"id":1001,"kind":"report","title":"Tide report","format":"report","artifact_id":70,"duration_seconds":null,"created_at":"2026-03-01T00:00:00Z"}],` +
			`"sources":[{"id":500,"title":"Moon.pdf","document_type":"FILE","has_file":true},` +
			`{"id":501,"title":"Sun notes","document_type":"NOTE","has_file":false},` +
			`{"id":502,"title":"In folder","document_type":"FILE","has_file":true}]}`
		if response.StatusCode != 200 || strings.TrimSpace(body) != want {
			t.Errorf("the page for %q: %d\n got %s\nwant %s", who, response.StatusCode, body, want)
		}
		for name, value := range map[string]string{
			"Cache-Control": "no-store", "X-Robots-Tag": "noindex, nofollow, noarchive",
			"Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff",
		} {
			if response.Header.Get(name) != value {
				t.Errorf("the page's %s is %q", name, response.Header.Get(name))
			}
		}
		for _, secret := range []string{alice, "alice@example.test", "Owner-only instructions", "hidden query", "older chat",
			"archived chat", "studio prompt", "another notebook", "Draft", "Going", "Diary", "storage", "documents/"} {
			if strings.Contains(body, secret) {
				t.Errorf("the page discloses %q", secret)
			}
		}
	}

	// A source opens as its file when the app has a viewer, else as text.
	response, body := w.as("", "GET", base+"/sources/500", "")
	w.expect("a PDF source", response, body, 200,
		`{"id":500,"title":"Moon.pdf","document_type":"FILE","presentation":"original","file":{"file_id":90,"filename":"Moon.pdf","mime_type":"application/pdf","size_bytes":12,"content_url":"`+base+`/sources/500/files/90/content"},"markdown":""}`)
	response, body = w.as("", "GET", base+"/sources/501", "")
	w.expect("a note source", response, body, 200, `"presentation":"text","file":null,"markdown":"# Sun"`)
	response, body = w.as("", "GET", base+"/sources/502", "")
	w.expect("a folder's document", response, body, 200, `"presentation":"text"`, `"mime_type":"text/html"`, `"markdown":"folder text"`)

	response, body = w.as("", "GET", base+"/sources/500/files/90/content", "", "Range", "bytes=0-3")
	w.expect("a PDF's first bytes", response, body, 206, "%PDF")
	if response.Header.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "inline") ||
		response.Header.Get("Cache-Control") != "private, no-cache" || response.Header.Get("Content-Security-Policy") != "" ||
		response.Header.Get("X-Robots-Tag") == "" {
		t.Errorf("a PDF's headers: %v", response.Header)
	}
	response, body = w.as("", "GET", base+"/sources/500/files/90/content", "", "If-None-Match", response.Header.Get("ETag"))
	w.expect("a PDF revalidated", response, body, 304)
	// HTML is somebody's script: never shown from this origin.
	response, body = w.as("", "GET", base+"/sources/502/files/93/content", "")
	w.expect("an HTML original", response, body, 200, "<script>x")
	if !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment") || response.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("an HTML original's headers: %v", response.Header)
	}

	// An output's manifest, file and download.
	response, body = w.as("", "GET", base+"/artifacts/71/manifest", "")
	w.expect("a video's manifest", response, body, 200,
		`{"artifact_id":71,"document_id":0,"title":"Tide film","format":"video","generation":1,"markdown_representation":"","files":[`+
			`{"file_id":81,"role":"primary","filename":"render-8f2c.mp4","mime_type":"video/mp4","size_bytes":10,"content_url":"`+base+`/artifacts/71/files/81/content"},`+
			`{"file_id":80,"role":"preview","filename":"poster.png","mime_type":"image/png","size_bytes":6,"content_url":"`+base+`/artifacts/71/files/80/content"}],`+
			`"updated_at":"2026-02-01T00:00:00Z"}`)
	response, body = w.as("", "GET", base+"/artifacts/71/files/81/content", "", "Range", "bytes=2-5")
	w.expect("a video's bytes by range", response, body, 206, "2345")
	if response.Header.Get("Content-Range") != "bytes 2-5/10" || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "inline") {
		t.Errorf("a video's headers: %v", response.Header)
	}
	response, body = w.as("", "GET", base+"/artifacts/71/download", "")
	w.expect("a video saved", response, body, 200, "0123456789")
	if disposition := response.Header.Get("Content-Disposition"); !strings.HasPrefix(disposition, `attachment; filename="Tide film.mp4"`) ||
		response.Header.Get("Cache-Control") != "private, no-store" || response.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("a saved video's headers: %v", response.Header)
	}
	response, body = w.as("", "GET", base+"/artifacts/70/download", "")
	w.expect("a report with no file, saved as its Markdown", response, body, 200, "# Report")
	if !strings.Contains(response.Header.Get("Content-Disposition"), `filename="Tide report.md"`) ||
		response.Header.Get("Content-Type") != "text/markdown; charset=utf-8" || response.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("a saved report's headers: %v", response.Header)
	}
	// What the shared chat saved is shown, by the chat's own card.
	response, body = w.as("", "GET", base+"/artifacts/72/manifest", "")
	w.expect("what the chat saved", response, body, 200, `"title":"Chat table"`)

	// Everything that exists and is outside this link's notebook is the
	// same 404 as something that does not exist.
	for name, path := range map[string]string{
		"a source of her other notebook":         "/sources/504",
		"that source's file":                     "/sources/504/files/92/content",
		"a shared source's redacted copy":        "/sources/500/files/91/content",
		"another document's file under a source": "/sources/500/files/92/content",
		"a source being deleted":                 "/sources/503",
		"a studio artifact added as a source":    "/sources/600",
		"the other account's document":           "/sources/700",
		"an unfinished output":                   "/artifacts/74/manifest",
		"an unfinished output's download":        "/artifacts/74/download",
		"another artifact's file":                "/artifacts/71/files/82/content",
		"an artifact that does not exist":        "/artifacts/999/manifest",
		"an id that is not a number":             "/sources/abc",
		"an id too wide to be one":               "/artifacts/99999999999999999999/manifest",
	} {
		response, body = w.as("", "GET", base+path, "")
		if response.StatusCode != 404 || !strings.Contains(body, `"message":"`+gone+`"`) || strings.Contains(body, "private") {
			t.Errorf("%s: %d %s", name, response.StatusCode, body)
		}
	}
	// The private deck is reachable only because the shared chat saved it;
	// through the other notebook's output alone it would not be. Bob's link
	// shows none of hers.
	response, body = w.as("", "GET", "/api/v1/public-notebooks/his-token/artifacts/73/manifest", "")
	w.expect("her artifact through his link", response, body, 404, gone)
	response, body = w.as("", "GET", "/api/v1/public-notebooks/his-token/sources/500", "")
	w.expect("her source through his link", response, body, 404, gone)

	// Tokens that name nothing, and a deactivated account's link.
	for name, token := range map[string]string{"an unknown token": "nope", "a token too long to be one": strings.Repeat("a", 129)} {
		response, body = w.as("", "GET", "/api/v1/public-notebooks/"+token, "")
		w.expect(name, response, body, 404, gone)
	}
	testpostgres.Exec(t, w.pool, `UPDATE "user" SET is_active = false WHERE id = '`+alice+`'`)
	for _, path := range []string{"", "/sources/500", "/sources/500/files/90/content", "/artifacts/71/manifest", "/artifacts/71/download"} {
		response, body = w.as("", "GET", base+path, "")
		w.expect("a deactivated account's link at "+path, response, body, 404, gone)
	}
}
