package publicshare

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/publicchat"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// databaseVariable names a throwaway Postgres. Every account in it is
// deleted (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_PUBLICSHARE_DATABASE"

// The clock every test runs at: the seeded expired link ended before it.
var testNow = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

// The bytes behind the seeded files, made up here.
const (
	pdfBytes     = "%PDF-1.7 0123456789 abcdefghij the end"
	htmlBytes    = "<script>alert(1)</script>"
	podcastBytes = "ID3 a podcast's audio"
	slideBytes   = "ID3 slide one"
	legacyBytes  = "ID3 an older podcast"
	bobsBytes    = "Bob's private file"
)

// world is the seeded database, the file store and a server over the routes.
// A request names its caller in a header; one with none has no caller at
// all, as a public route's request has behind the real front door.
type world struct {
	t      *testing.T
	pool   *pgxpool.Pool
	server *httptest.Server
}

func newWorld(t *testing.T) *world {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// Alice shares; Bob is the other account. Artifact 73 is Alice's and
	// not in any link; artifact 80 is Bob's and *is* named by Alice's link,
	// which is the forged list the owner check exists for.
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, source_markdown) VALUES
 (500, '`+alice+`', 'Tide report', 'ARTIFACT', 'plain', 'd500', '# Tides'||chr(10)||'Twice a day.'),
 (501, '`+alice+`', 'Tide talk', 'ARTIFACT', 'Host: hello', 'd501', NULL),
 (502, '`+alice+`', 'Tide film', 'ARTIFACT', '', 'd502', NULL),
 (503, '`+alice+`', 'Private', 'ARTIFACT', '', 'd503', NULL),
 (600, '`+bob+`',   'His', 'ARTIFACT', '', 'd600', NULL);
INSERT INTO artifacts (id, document_id, user_id, format, generation, metadata) VALUES
 (70, 500, '`+alice+`', 'report', 2, NULL),
 (71, 501, '`+alice+`', 'podcast', 1, NULL),
 (72, 502, '`+alice+`', 'video', 1, '{"slides":[
    {"slide_number":1,"audio_storage_key":"video/72/s1.mp3","storage_backend":"local"},
    {"slide_number":2,"audio_file":"/srv/render/s2.mp3"}],"scene_codes":[]}'),
 (73, 503, '`+alice+`', 'report', 1, NULL),
 (80, 600, '`+bob+`',   'report', 1, NULL);
INSERT INTO artifact_files (id, artifact_id, role, storage_backend, storage_key, original_filename, mime_type, size_bytes, checksum_sha256) VALUES
 (700, 70, 'primary', 'local', 'artifacts/70/report.pdf', 'Tide report.pdf', 'application/pdf', 38, 'c700'),
 (701, 70, 'preview', 'local', 'artifacts/70/report.html', 'report.html', 'text/html', 25, 'c701'),
 (710, 71, 'primary', 'local', 'artifacts/71/talk.mp3', 'talk.mp3', 'audio/mpeg', 21, 'c710'),
 (730, 73, 'primary', 'local', 'artifacts/73/private.pdf', 'private.pdf', 'application/pdf', 38, 'c730'),
 (800, 80, 'primary', 'local', 'artifacts/80/his.pdf', 'his.pdf', 'application/pdf', 18, 'c800');
INSERT INTO notebooks (id, user_id, title) VALUES (10, '`+alice+`', 'Tides'), (20, '`+bob+`', 'His');
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, phase, progress, artifact_id) VALUES
 (80, '`+alice+`', 'report', 'Tide report', 't1', 'ready', 'done', 100, 70),
 (81, '`+alice+`', 'report', 'Draft', 't2', 'running', 'writing', 40, NULL),
 (82, '`+bob+`',   'report', 'His', 't3', 'ready', 'done', 100, 80);
INSERT INTO podcast_runs (id, user_id, title, status, artifact_id) VALUES (90, '`+alice+`', 'Tide talk', 'ready', 71);
INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title, deliverable_job_id, podcast_id) VALUES
 (1001, 10, '`+alice+`', 'report', 'Tide report', 80, NULL),
 (1002, 10, '`+alice+`', 'report', 'Draft', 81, NULL),
 (1003, 10, '`+alice+`', 'audio', 'Tide talk', NULL, 90),
 (2001, 20, '`+bob+`',   'report', 'His', 82, NULL);
INSERT INTO new_chat_threads (id, user_id, title) VALUES (30, '`+alice+`', 'About tides');
INSERT INTO new_chat_messages (id, thread_id, role, content, author_id) VALUES
 (300, 30, 'USER', '[{"type":"text","text":"Why two tides?"}]', '`+alice+`'),
 (301, 30, 'ASSISTANT', '[{"type":"text","text":"The moon. private note"}]', NULL);
INSERT INTO public_chat_snapshots (id, user_id, thread_id, share_token, content_hash, snapshot_data, message_ids, expires_at) VALUES
 (40, '`+alice+`', 30, 'chat-live', 'h40', '{
   "title":"About tides","snapshot_at":"2026-11-01T10:00:00+00:00",
   "author":{"display_name":"Alice","avatar_url":null},
   "messages":[
     {"id":300,"role":"user","content":[{"type":"text","text":"Why two tides?"}],"author":{"display_name":"Alice","avatar_url":null},"created_at":"2026-11-01T09:00:00+00:00"},
     {"id":301,"role":"assistant","content":[{"type":"text","text":"The moon."},{"type":"tool-call","toolName":"generate_podcast","result":{"podcast_id":90,"artifact_id":71,"status":"ready"}}],"author":null,"created_at":"2026-11-01T09:00:05+00:00"},
     {"id":302,"role":"narrator","content":[],"author":null,"created_at":null}],
   "podcasts":[
     {"original_id":90,"title":"Tide talk","transcript":null,"artifact_id":71},
     {"original_id":91,"title":"Older talk","storage_key":"podcasts/legacy/91.mp3"},
     {"original_id":92,"title":"Oldest talk","file_path":"/srv/podcasts/92.mp3"}],
   "video_presentations":[{"original_id":5,"title":"Old film","user_id":"`+alice+`",
     "slides":[
       {"slide_number":1,"title":"One","duration_in_frames":300,"audio_storage_key":"video/72/s1.mp3","storage_backend":"local","audio_file":"/srv/render/s1.mp3"},
       {"slide_number":2,"title":"Two","audio_file":"/srv/render/s2.mp3"}],
     "scene_codes":[{"slide_number":1,"code":"scene()"}]}],
   "artifact_ids":[70,71,72,80]}', '{300,301}', NULL),
 (41, '`+alice+`', 30, 'chat-expired', 'h41', '{"title":"About tides","messages":[],"podcasts":[{"original_id":90,"artifact_id":71}],"artifact_ids":[70,71,72]}', '{300}', '2026-11-12 08:59:59+00');`)

	directory := t.TempDir()
	for key, content := range map[string]string{
		"artifacts/70/report.pdf":  pdfBytes,
		"artifacts/70/report.html": htmlBytes,
		"artifacts/71/talk.mp3":    podcastBytes,
		"artifacts/73/private.pdf": pdfBytes,
		"artifacts/80/his.pdf":     bobsBytes,
		"video/72/s1.mp3":          slideBytes,
		"podcasts/legacy/91.mp3":   legacyBytes,
	} {
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

	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	Routes{Pool: pool, Files: stores, StallTimeout: time.Minute, FrontendURL: "https://notes.example.test/",
		Errors: errorenvelope.Writer{}, Logger: logger, Now: func() time.Time { return testNow }}.Register(api)
	// The page route a link opens with, so a link this package makes is
	// read back the way the web page reads it.
	publicchat.PageRoute{Rows: publicchat.Store{Pool: pool}, Errors: errorenvelope.Writer{}, Logger: logger,
		Now: func() time.Time { return testNow }}.Register(api)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if caller := request.Header.Get("X-Test-Caller"); caller != "" {
			method := principal.MethodSession
			if request.Header.Get("X-Test-Key") != "" {
				method = principal.MethodPersonalAccessToken
			}
			request = request.WithContext(principal.WithPrincipal(request.Context(),
				principal.Principal{UserID: caller, Method: method}))
		}
		mux.ServeHTTP(response, request)
	}))
	t.Cleanup(server.Close)
	return &world{t: t, pool: pool, server: server}
}

// ask sends one request. caller is an account id, or "" for a stranger.
func (w *world) ask(caller, method, path, body string, headers ...string) (*http.Response, string) {
	w.t.Helper()
	request, err := http.NewRequest(method, w.server.URL+path, strings.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	if caller != "" {
		request.Header.Set("X-Test-Caller", caller)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		w.t.Fatal(err)
	}
	defer response.Body.Close()
	answered, err := io.ReadAll(response.Body)
	if err != nil {
		w.t.Fatal(err)
	}
	return response, string(answered)
}

func (w *world) count(query string, arguments ...any) (n int) {
	w.t.Helper()
	if err := w.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+query, arguments...).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

func TestALinksFilesAreReachedByItsTokenAlone(t *testing.T) {
	w := newWorld(t)
	const base = "/api/v1/public/chat-live"

	t.Run("a stranger reads the main file, with ranges and a validator", func(t *testing.T) {
		response, body := w.ask("", "GET", base+"/artifacts/70/content", "")
		if response.StatusCode != 200 || body != pdfBytes {
			t.Fatalf("%d %q", response.StatusCode, body)
		}
		for name, want := range map[string]string{
			"Content-Type":           "application/pdf",
			"Cache-Control":          "private, no-cache",
			"Etag":                   `"c700"`,
			"X-Content-Type-Options": "nosniff",
			"Accept-Ranges":          "bytes",
		} {
			if got := response.Header.Get(name); got != want {
				t.Errorf("%s is %q, want %q", name, got, want)
			}
		}
		if disposition := response.Header.Get("Content-Disposition"); !strings.HasPrefix(disposition, "inline;") {
			t.Errorf("a PDF should open in the browser: %q", disposition)
		}
		if response.Header.Get("Content-Security-Policy") != "" {
			t.Error("a PDF is not sandboxed: the browser's own viewer would not run")
		}
		response, body = w.ask("", "GET", base+"/artifacts/70/content", "", "Range", "bytes=9-18")
		if response.StatusCode != 206 || body != "0123456789" || response.Header.Get("Content-Range") != "bytes 9-18/38" {
			t.Errorf("a range: %d %q %q", response.StatusCode, body, response.Header.Get("Content-Range"))
		}
		response, body = w.ask("", "GET", base+"/artifacts/70/content", "", "If-None-Match", `"c700"`)
		if response.StatusCode != 304 || body != "" {
			t.Errorf("a repeat view should be a 304: %d %q", response.StatusCode, body)
		}
	})

	t.Run("a file a browser could run is sent to be saved, sandboxed", func(t *testing.T) {
		response, body := w.ask("", "GET", base+"/artifacts/70/files/701/content", "")
		if response.StatusCode != 200 || body != htmlBytes {
			t.Fatalf("%d %q", response.StatusCode, body)
		}
		if response.Header.Get("Content-Security-Policy") != "sandbox" ||
			!strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment;") {
			t.Errorf("HTML was sent as a page of this site: %v", response.Header)
		}
	})

	t.Run("nothing outside the link is reached", func(t *testing.T) {
		for name, path := range map[string]string{
			"her artifact the link does not list":           base + "/artifacts/73/content",
			"his artifact, though the link's list names it": base + "/artifacts/80/content",
			"his file by id":                 base + "/artifacts/80/files/800/content",
			"a file of another artifact":     base + "/artifacts/70/files/730/content",
			"an artifact that never existed": base + "/artifacts/999/content",
			"an expired link":                "/api/v1/public/chat-expired/artifacts/70/content",
			"an expired link's file by id":   "/api/v1/public/chat-expired/artifacts/70/files/700/content",
			"a token that names nothing":     "/api/v1/public/no-such-link/artifacts/70/content",
			"an id too wide to be a row":     base + "/artifacts/99999999999999999999/content",
		} {
			response, body := w.ask("", "GET", path, "")
			if response.StatusCode != 404 || !strings.Contains(body, artifactAbsent) {
				t.Errorf("%s: %d %s", name, response.StatusCode, body)
			}
			if strings.Contains(body, bobsBytes) || strings.Contains(body, pdfBytes) {
				t.Errorf("%s: bytes were sent", name)
			}
		}
		response, body := w.ask("", "GET", base+"/artifacts/seventy/content", "")
		if response.StatusCode != 422 {
			t.Errorf("an id that is not a number: %d %s", response.StatusCode, body)
		}
	})

	t.Run("a video slide's narration, from the store and never from a path", func(t *testing.T) {
		response, body := w.ask("", "GET", base+"/artifacts/72/slides/1/audio", "")
		if response.StatusCode != 200 || body != slideBytes || response.Header.Get("Content-Type") != "audio/mpeg" {
			t.Errorf("%d %q %q", response.StatusCode, body, response.Header.Get("Content-Type"))
		}
		if disposition := response.Header.Get("Content-Disposition"); !strings.Contains(disposition, "slide-1.mp3") {
			t.Errorf("the audio is named for where it is stored: %q", disposition)
		}
		for name, path := range map[string]string{
			"a slide that names only a server path": base + "/artifacts/72/slides/2/audio",
			"a slide that is not there":             base + "/artifacts/72/slides/9/audio",
			"an artifact that is not a video":       base + "/artifacts/70/slides/1/audio",
			"an expired link":                       "/api/v1/public/chat-expired/artifacts/72/slides/1/audio",
		} {
			if response, body := w.ask("", "GET", path, ""); response.StatusCode != 404 || strings.Contains(body, "/srv/") {
				t.Errorf("%s: %d %s", name, response.StatusCode, body)
			}
		}
	})

	t.Run("a shared podcast's audio by the podcast's id", func(t *testing.T) {
		response, body := w.ask("", "GET", base+"/podcasts/90/stream", "", "Range", "bytes=0-2")
		if response.StatusCode != 206 || body != "ID3" || response.Header.Get("Content-Type") != "audio/mpeg" {
			t.Errorf("the artifact's audio: %d %q", response.StatusCode, body)
		}
		response, body = w.ask("", "GET", base+"/podcasts/91/stream", "")
		if response.StatusCode != 200 || body != legacyBytes {
			t.Errorf("an older link's stored audio: %d %q", response.StatusCode, body)
		}
		for name, path := range map[string]string{
			"a podcast frozen with a server path": base + "/podcasts/92/stream",
			"a podcast the link does not hold":    base + "/podcasts/99/stream",
			"an expired link":                     "/api/v1/public/chat-expired/podcasts/90/stream",
		} {
			if response, body := w.ask("", "GET", path, ""); response.StatusCode != 404 || strings.Contains(body, "/srv/") {
				t.Errorf("%s: %d %s", name, response.StatusCode, body)
			}
		}
	})

	t.Run("a frozen presentation, with nothing internal in it", func(t *testing.T) {
		response, body := w.ask("", "GET", base+"/video-presentations/5", "")
		if response.StatusCode != 200 {
			t.Fatalf("%d %s", response.StatusCode, body)
		}
		var payload struct {
			ID     int64  `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
			Slides []struct {
				SlideNumber int     `json:"slide_number"`
				AudioURL    *string `json:"audio_url"`
			} `json:"slides"`
			SceneCodes []map[string]any `json:"scene_codes"`
			SlideCount int              `json:"slide_count"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.ID != 5 || payload.Title != "Old film" || payload.Status != "ready" || payload.SlideCount != 2 ||
			len(payload.Slides) != 2 || len(payload.SceneCodes) != 1 {
			t.Errorf("the payload: %s", body)
		}
		if payload.Slides[0].AudioURL == nil || *payload.Slides[0].AudioURL != base+"/video-presentations/5/slides/1/audio" ||
			payload.Slides[1].AudioURL != nil {
			t.Errorf("only stored narration has an address: %s", body)
		}
		for _, internal := range []string{"/srv/", "audio_storage_key", "storage_backend", "audio_file", "user_id", testpostgres.Alice} {
			if strings.Contains(body, internal) {
				t.Errorf("the payload discloses %q: %s", internal, body)
			}
		}
		response, body = w.ask("", "GET", *payload.Slides[0].AudioURL, "")
		if response.StatusCode != 200 || body != slideBytes {
			t.Errorf("the address it gave: %d %q", response.StatusCode, body)
		}
		for name, path := range map[string]string{
			"a slide with only a server path":   base + "/video-presentations/5/slides/2/audio",
			"a presentation the link lacks":     base + "/video-presentations/6",
			"a presentation of an expired link": "/api/v1/public/chat-expired/video-presentations/5",
		} {
			if response, body := w.ask("", "GET", path, ""); response.StatusCode != 404 {
				t.Errorf("%s: %d %s", name, response.StatusCode, body)
			}
		}
	})

	t.Run("a revoked link serves nothing more", func(t *testing.T) {
		testpostgres.Exec(t, w.pool, `DELETE FROM public_chat_snapshots WHERE share_token = 'chat-live'`)
		for _, path := range []string{
			base + "/artifacts/70/content", base + "/artifacts/70/files/700/content",
			base + "/artifacts/72/slides/1/audio", base + "/podcasts/90/stream", base + "/video-presentations/5",
		} {
			// The validator a browser kept does not help it either.
			if response, body := w.ask("", "GET", path, "", "If-None-Match", `"c700"`); response.StatusCode != 404 {
				t.Errorf("%s after the revoke: %d %s", path, response.StatusCode, body)
			}
		}
	})
}

func TestACopyOfASharedChatIsTheCallersOwn(t *testing.T) {
	w := newWorld(t)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	const clone = "/api/v1/public/chat-live/clone"

	for name, refused := range map[string]struct {
		caller, path string
		headers      []string
		status       int
		says         string
	}{
		"an expired link":            {bob, "/api/v1/public/chat-expired/clone", nil, 404, linkAbsent},
		"a token that names nothing": {bob, "/api/v1/public/no-such-link/clone", nil, 404, linkAbsent},
		"an API key":                 {bob, clone, []string{"X-Test-Key", "1"}, 403, sessionRequired},
	} {
		response, body := w.ask(refused.caller, "POST", refused.path, "", refused.headers...)
		if response.StatusCode != refused.status || !strings.Contains(body, refused.says) {
			t.Errorf("%s: %d %s", name, response.StatusCode, body)
		}
	}
	if w.count(`new_chat_threads WHERE user_id = $1`, bob) != 0 || w.count(`notebooks WHERE user_id = $1`, bob) != 1 {
		t.Fatal("a refused copy wrote something")
	}

	response, body := w.ask(bob, "POST", clone, "")
	if response.StatusCode != 200 {
		t.Fatalf("his copy: %d %s", response.StatusCode, body)
	}
	var first copied
	if err := json.Unmarshal([]byte(body), &first); err != nil || first.ThreadID == 0 || first.NotebookID == 0 {
		t.Fatalf("his copy's answer: %s (%v)", body, err)
	}
	// Everything the copy made is his.
	if w.count(`new_chat_threads WHERE id = $1 AND user_id = $2 AND notebook_id = $3 AND title = 'About tides'
		AND cloned_from_thread_id = 30 AND cloned_from_snapshot_id = 40 AND cloned_at IS NOT NULL`, first.ThreadID, bob, first.NotebookID) != 1 {
		t.Error("the copied thread is not his, in his notebook, recording where it came from")
	}
	if w.count(`notebooks WHERE id = $1 AND user_id = $2 AND title = $3`, first.NotebookID, bob, earlierChatsTitle) != 1 ||
		w.count(`notebook_earlier_chats WHERE thread_id = $1 AND notebook_id = $2 AND user_id = $3`, first.ThreadID, first.NotebookID, bob) != 1 {
		t.Error("the copy is not filed in a notebook of his own")
	}
	// The public copy's messages, in order, and only those with a real role;
	// what the public copy left out (the private note) is not in his copy.
	rows, err := w.pool.Query(context.Background(),
		`SELECT role::text, content::text, author_id IS NULL FROM new_chat_messages WHERE thread_id = $1 ORDER BY created_at, id`, first.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for rows.Next() {
		var role, content string
		var noAuthor bool
		if err := rows.Scan(&role, &content, &noAuthor); err != nil {
			t.Fatal(err)
		}
		if !noAuthor {
			t.Error("a copied message names an author")
		}
		if strings.Contains(content, "private note") {
			t.Error("the copy holds text the public copy did not")
		}
		seen = append(seen, role)
	}
	rows.Close()
	if strings.Join(seen, ",") != "USER,ASSISTANT" {
		t.Errorf("the copied messages are %v", seen)
	}
	// Hers are as they were.
	if w.count(`new_chat_threads WHERE user_id = $1`, alice) != 1 || w.count(`new_chat_messages WHERE thread_id = 30`) != 2 ||
		w.count(`notebooks WHERE user_id = $1`, alice) != 1 || w.count(`public_chat_snapshots WHERE user_id = $1`, alice) != 2 {
		t.Error("the copy changed the sharer's rows")
	}

	// A second copy is a second thread in the same notebook.
	response, body = w.ask(bob, "POST", clone, "")
	var second copied
	if err := json.Unmarshal([]byte(body), &second); err != nil || response.StatusCode != 200 {
		t.Fatalf("his second copy: %d %s", response.StatusCode, body)
	}
	if second.NotebookID != first.NotebookID || second.ThreadID == first.ThreadID ||
		w.count(`notebooks WHERE user_id = $1 AND title = $2`, bob, earlierChatsTitle) != 1 {
		t.Errorf("a second copy made a second notebook: %+v then %+v", first, second)
	}

	// A link to a Studio output is not a chat.
	response, body = w.ask(alice, "POST", "/api/v1/notebooks/10/studio/outputs/1001/public-link", "")
	var link createdLink
	if err := json.Unmarshal([]byte(body), &link); err != nil || response.StatusCode != 200 {
		t.Fatalf("her output link: %d %s", response.StatusCode, body)
	}
	response, body = w.ask(bob, "POST", "/api/v1/public/"+link.ShareToken+"/clone", "")
	if response.StatusCode != 400 || !strings.Contains(body, notAChat) {
		t.Errorf("copying an output link: %d %s", response.StatusCode, body)
	}
}

func TestAnOutputIsSharedByItsOwnerAlone(t *testing.T) {
	w := newWorld(t)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	const hers = "/api/v1/notebooks/10/studio/outputs/1001/public-link"
	share := func(caller, path, body string) (int, createdLink, string) {
		t.Helper()
		response, answered := w.ask(caller, "POST", path, body)
		var link createdLink
		if response.StatusCode == 200 {
			if err := json.Unmarshal([]byte(answered), &link); err != nil {
				t.Fatalf("%s: %v", answered, err)
			}
		}
		return response.StatusCode, link, answered
	}

	for name, refused := range map[string]struct {
		caller, path, body string
		status             int
		says               string
	}{
		"his request for her output":            {bob, hers, "", 404, outputAbsent},
		"her output through his notebook":       {bob, "/api/v1/notebooks/20/studio/outputs/1001/public-link", "", 404, outputAbsent},
		"his output through her notebook":       {alice, "/api/v1/notebooks/10/studio/outputs/2001/public-link", "", 404, outputAbsent},
		"an output that is still being written": {alice, "/api/v1/notebooks/10/studio/outputs/1002/public-link", "", 400, outputUnfinished},
		"an expiry that is not offered":         {alice, hers, `{"expires_in_days":365}`, 422, badExpiry},
		"a body that is not JSON":               {alice, hers, `seven`, 422, badExpiry},
	} {
		status, _, body := share(refused.caller, refused.path, refused.body)
		if status != refused.status || !strings.Contains(body, refused.says) {
			t.Errorf("%s: %d %s", name, status, body)
		}
	}
	if w.count(`public_chat_snapshots WHERE notebook_output_id IS NOT NULL`) != 0 {
		t.Fatal("a refused share made a link")
	}

	status, first, body := share(alice, hers, "")
	if status != 200 || !first.IsNew || first.ShareToken == "" ||
		first.PublicURL != "https://notes.example.test/public/"+first.ShareToken ||
		first.ExpiresAt == nil || *first.ExpiresAt != "2026-11-19T09:00:00Z" {
		t.Fatalf("her first share: %d %s", status, body)
	}
	if w.count(`public_chat_snapshots WHERE id = $1 AND user_id = $2 AND notebook_output_id = 1001 AND thread_id IS NULL`, first.SnapshotID, alice) != 1 {
		t.Error("the link is not hers, or not the output's")
	}

	// The page the link opens, as the web reads it: the text as shared and
	// every file of the artifact at this link's own addresses.
	response, page := w.ask("", "GET", "/api/v1/public/"+first.ShareToken, "")
	if response.StatusCode != 200 {
		t.Fatalf("the page: %d %s", response.StatusCode, page)
	}
	for _, piece := range []string{
		`"kind":"output"`, `"title":"Tide report"`, `"format":"report"`, `"markdown":"# Tides\nTwice a day."`,
		`"artifact_id":70`, `"generation":2`,
		`"file":{"filename":"Tide report.pdf","mime_type":"application/pdf","size_bytes":38,"display":"pdf"}`,
		`"file_id":700,"role":"primary"`, `"file_id":701,"role":"preview"`,
		`"content_url":"/api/v1/public/` + first.ShareToken + `/artifacts/70/files/700/content"`,
	} {
		if !strings.Contains(page, piece) {
			t.Errorf("the page lacks %s: %s", piece, page)
		}
	}
	for _, internal := range []string{"artifacts/70/report.pdf", "c700", "alice@example.test", alice} {
		if strings.Contains(page, internal) {
			t.Errorf("the page discloses %q", internal)
		}
	}
	// And the address it gave serves the file to a stranger; her other
	// artifact is still out of reach through it.
	if response, bytes := w.ask("", "GET", "/api/v1/public/"+first.ShareToken+"/artifacts/70/files/700/content", ""); response.StatusCode != 200 || bytes != pdfBytes {
		t.Errorf("the file at the link's address: %d %q", response.StatusCode, bytes)
	}
	if response, _ := w.ask("", "GET", "/api/v1/public/"+first.ShareToken+"/artifacts/71/content", ""); response.StatusCode != 404 {
		t.Errorf("an artifact the output link does not share: %d", response.StatusCode)
	}

	// Shared again unchanged, after a rename: the same address, the new
	// title, the expiry just chosen.
	testpostgres.Exec(t, w.pool, `UPDATE notebook_outputs SET title = 'Tides, final' WHERE id = 1001`)
	status, again, body := share(alice, hers, `{"expires_in_days":null}`)
	if status != 200 || again.IsNew || again.ShareToken != first.ShareToken || again.SnapshotID != first.SnapshotID || again.ExpiresAt != nil {
		t.Errorf("her second share: %d %s", status, body)
	}
	if w.count(`public_chat_snapshots WHERE id = $1 AND snapshot_data ->> 'title' = 'Tides, final'
		AND snapshot_data #>> '{output,title}' = 'Tides, final' AND expires_at IS NULL`, first.SnapshotID) != 1 {
		t.Error("the renewed link did not take the new title and expiry")
	}

	// An expired link stays dead: the same content gets a new address.
	testpostgres.Exec(t, w.pool, `UPDATE public_chat_snapshots SET expires_at = '2026-11-12 08:00:00+00' WHERE notebook_output_id = 1001`)
	if response, _ := w.ask("", "GET", "/api/v1/public/"+first.ShareToken+"/artifacts/70/content", ""); response.StatusCode != 404 {
		t.Errorf("an expired output link still serves its file: %d", response.StatusCode)
	}
	status, fresh, body := share(alice, hers, `{"expires_in_days":30}`)
	if status != 200 || !fresh.IsNew || fresh.ShareToken == first.ShareToken ||
		fresh.ExpiresAt == nil || *fresh.ExpiresAt != "2026-12-12T09:00:00Z" {
		t.Errorf("sharing after the expiry: %d %s", status, body)
	}
	if w.count(`public_chat_snapshots WHERE notebook_output_id = 1001`) != 1 {
		t.Error("the expired link's row was kept beside the new one")
	}

	// Changed content is a new link, and the old one keeps its copy.
	testpostgres.Exec(t, w.pool, `UPDATE artifacts SET generation = 3 WHERE id = 70`)
	status, changed, body := share(alice, hers, "")
	if status != 200 || !changed.IsNew || changed.ShareToken == fresh.ShareToken || w.count(`public_chat_snapshots WHERE notebook_output_id = 1001`) != 2 {
		t.Errorf("sharing a changed output: %d %s", status, body)
	}

	// An audio overview is shared by the podcast run that made it.
	status, audio, body := share(alice, "/api/v1/notebooks/10/studio/outputs/1003/public-link", "{}")
	if status != 200 || !audio.IsNew {
		t.Fatalf("sharing an audio overview: %d %s", status, body)
	}
	if response, bytes := w.ask("", "GET", "/api/v1/public/"+audio.ShareToken+"/artifacts/71/content", ""); response.StatusCode != 200 || bytes != podcastBytes {
		t.Errorf("the shared audio: %d %q", response.StatusCode, bytes)
	}
}
