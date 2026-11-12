package notebookresearch

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// localOriginals keeps imported PDFs on a disk of the test's own.
func localOriginals(t *testing.T) Originals {
	t.Helper()
	files, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Originals{Files: files, MaxBytes: 1 << 20}
}

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_NOTEBOOKRESEARCH_DATABASE"

// harness is the four routes over a real listener (a stream's write
// deadline can only be moved on a real connection) and a real database,
// with stand-ins for everything outside the process.
type harness struct {
	t        *testing.T
	pool     *pgxpool.Pool
	server   *httptest.Server
	reads    *Reads
	indexing *indexing
	pages    *pages
	papers   *papers
}

func newHarness(t *testing.T) *harness {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title, updated_at) VALUES
 (10, '`+alice+`', 'Tides', '2026-01-01'), (20, '`+bob+`', 'His', '2026-01-01');`)
	h := &harness{t: t, pool: pool, indexing: &indexing{},
		pages: &pages{byAddress: map[string]pagereader.Page{
			"https://tides.example/tables": {Status: pagereader.Read, Title: "Tide tables", Text: "Heights by port.", Notes: []string{"Cut: the page is long."}},
		}},
		papers: &papers{pubmed: []academic.Paper{tidesPaper}, fullText: map[string]string{"PMC900": "Methods\nWe watched the sea."}},
	}
	reads, err := NewReads(context.Background(), 2, pool, h.pages, h.papers, h.indexing, localOriginals(t), quiet())
	if err != nil {
		t.Fatal(err)
	}
	h.reads = reads
	routes := Routes{
		Pool: pool, Papers: h.papers, Pages: h.pages, Reads: reads, Stall: 5 * time.Second,
		Parsers: parserchoicetest.Registry(t, parse.Local, parse.Datalab),
		// Only Alice has connected Exa.
		Web:    web{connected: map[string]map[websearch.Provider]bool{alice: {websearch.Exa: true}}},
		Errors: errorenvelope.Writer{}, Logger: quiet(),
		Now: func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) },
	}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	// The gate's place: the caller is whoever the test says.
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.Adapter().ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(),
			principal.Principal{UserID: r.Header.Get("X-Test-User"), Method: principal.MethodSession})))
	}))
	t.Cleanup(h.server.Close)
	return h
}

func (h *harness) as(userID, method, path, body string) (int, string) {
	h.t.Helper()
	request, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	request.Header.Set("X-Test-User", userID)
	response, err := h.server.Client().Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(raw)
}

func (h *harness) count(query string) (n int) {
	h.t.Helper()
	if err := h.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+query).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// settle waits for the reads an import started to record how they ended.
func (h *harness) settle() {
	h.t.Helper()
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err := h.reads.Stop(ctx); err != nil {
		h.t.Fatal(err)
	}
}

func expect(t *testing.T, name string, status int, body string, want int, says ...string) {
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

func TestResearchIsConfinedToTheCallersNotebookAndDocuments(t *testing.T) {
	h := newHarness(t)
	const alice, bob = testpostgres.Alice, testpostgres.Bob

	// What each may search: Exa is greyed out for the account without a
	// key, Scholar for everyone, and nothing says whose key exists.
	status, body := h.as(alice, "GET", "/api/v1/notebooks/10/research/sources", "")
	expect(t, "her sources", status, body, 200, `{"unavailable":{"google_scholar":"not_available","tavily":"not_connected"},"page_reader":"crawler"}`)
	status, body = h.as(bob, "GET", "/api/v1/notebooks/20/research/sources", "")
	expect(t, "his sources", status, body, 200, `"exa":"not_connected"`, `"tavily":"not_connected"`)
	status, body = h.as(bob, "GET", "/api/v1/notebooks/10/research/sources", "")
	expect(t, "her notebook asked by him", status, body, 404, notebookAbsent)
	status, body = h.as(alice, "GET", "/api/v1/notebooks/ten/research/sources", "")
	expect(t, "an id that is not a number", status, body, 422, "notebook_id")

	// A search streams events; another account's notebook is a 404 before
	// any source is asked.
	status, body = h.as(alice, "POST", "/api/v1/notebooks/10/research/search", `{"query":"tides","source":"pubmed"}`)
	expect(t, "her search", status, body, 200, `data: {"type":"phase","phase":"searching","progress":5}`,
		`"type":"results"`, `"title":"Tides and the moon"`, "data: {\"type\":\"done\"}\n\n")
	asked := len(h.papers.asked)
	status, body = h.as(bob, "POST", "/api/v1/notebooks/10/research/search", `{"query":"tides","source":"pubmed"}`)
	expect(t, "his search in her notebook", status, body, 404, notebookAbsent)
	status, body = h.as(alice, "POST", "/api/v1/notebooks/10/research/search", `{"query":"tides","source":"reddit"}`)
	expect(t, "a source that was cut", status, body, 422, "source")
	if len(h.papers.asked) != asked {
		t.Error("a refused search still asked a source")
	}

	// An import: a web page and a paper, each a document of hers and a
	// ticked source of her notebook, listed at once.
	importBody := `{"items":[
 {"url":"https://tides.example/tables","title":"Tables","snippet":"A teaser.","source":"exa"},
 {"url":"https://pubmed.ncbi.nlm.nih.gov/111/","title":"Tides and the moon","source":"pubmed","paper":{"pmid":"111","pmcid":"PMC900","doi":"10.1000/tides"}},
 {"url":"https://gone.example/","title":"Gone","snippet":"What the search said."}]}`
	status, body = h.as(bob, "POST", "/api/v1/notebooks/10/research/import", importBody)
	expect(t, "his import into her notebook", status, body, 404, notebookAbsent)
	if h.count("documents") != 0 || h.count("notebook_sources") != 0 {
		t.Fatal("a refused import wrote rows")
	}
	status, body = h.as(alice, "POST", "/api/v1/notebooks/10/research/import", importBody)
	expect(t, "her import", status, body, 200, `"title":"Tables"`, `"document_type":"CRAWLED_URL"`, `"kind":"document"`, `"enabled":true`)
	var listed []map[string]any
	if err := json.Unmarshal([]byte(body), &listed); err != nil || len(listed) != 3 {
		t.Fatalf("her sources after the import: %v %s", err, body)
	}
	if h.count("documents WHERE user_id = '"+alice+"'") != 3 || h.count("notebook_sources WHERE notebook_id = 10 AND user_id = '"+alice+"'") != 3 ||
		h.count("notebooks WHERE id = 10 AND updated_at = '2026-11-12 09:00:00+00'") != 1 {
		t.Error("her import did not make three documents, three sources and mark the notebook used")
	}

	// The reads: the page's text, the paper's open full text, and for a
	// page that could not be read the title and snippet, saying so.
	h.settle()
	if h.count(`documents WHERE title = 'Tables' AND source_markdown = E'# Tide tables\n\nHeights by port.' AND content = source_markdown
		AND status ->> 'state' = 'pending' AND NOT status ? 'stage' AND content_needs_reindexing
		AND document_metadata ->> 'fetched' = 'true' AND document_metadata ->> 'read_from' = 'page'
		AND document_metadata -> 'read_notes' ->> 0 LIKE 'Cut:%' AND document_metadata ->> 'research_source' = 'exa'`) != 1 {
		t.Error("the page was not stored as read")
	}
	if h.count(`documents WHERE title = 'Tides and the moon' AND source_markdown = E'# Tides and the moon\n\nMethods\nWe watched the sea.'
		AND document_metadata ->> 'read_from' = 'pubmed_central' AND document_metadata -> 'paper' ->> 'pmid' = '111'`) != 1 {
		t.Error("the paper was not stored from its open full text")
	}
	if h.count(`documents WHERE title = 'Gone' AND source_markdown = E'# Gone\n\nWhat the search said.\n\nhttps://gone.example/'
		AND document_metadata ->> 'fetched' = 'false' AND document_metadata ->> 'fetch_problem' = 'The page could not be reached.'`) != 1 {
		t.Error("the unreadable page does not say it was not read, or lost its snippet")
	}
	if len(h.indexing.queued) != 3 {
		t.Errorf("indexing was queued for %v, want her three documents", h.indexing.queued)
	}
	for _, user := range h.indexing.users {
		if user != alice {
			t.Errorf("a job was queued for %s", user)
		}
	}

	// Importing again links the same documents and reads nothing twice:
	// the same page under a different spelling, and the same paper found
	// on another source under another address.
	reads := len(h.pages.read)
	status, body = h.as(alice, "POST", "/api/v1/notebooks/10/research/import", `{"items":[
 {"url":"http://www.tides.example/tables/?utm_source=mail","title":"Tables again"},
 {"url":"https://doi.org/10.1000/tides","title":"The same paper","source":"medrxiv","paper":{"doi":"10.1000/TIDES"}}]}`)
	expect(t, "her second import", status, body, 200)
	h.settle()
	if h.count("documents WHERE user_id = '"+alice+"'") != 3 || h.count("notebook_sources WHERE notebook_id = 10") != 3 || len(h.pages.read) != reads {
		t.Error("a page or paper she already holds was imported twice")
	}

	// A source she switched off is switched back on by importing it again.
	testpostgres.Exec(t, h.pool, `UPDATE notebook_sources SET enabled = false WHERE notebook_id = 10`)
	h.as(alice, "POST", "/api/v1/notebooks/10/research/import", `{"items":[{"url":"https://tides.example/tables","title":"Tables"}]}`)
	if h.count("notebook_sources WHERE notebook_id = 10 AND enabled") != 1 {
		t.Error("importing a switched-off source did not switch it back on")
	}

	// The other account importing the same page gets a document of its
	// own; hers is never linked into his notebook.
	status, body = h.as(bob, "POST", "/api/v1/notebooks/20/research/import", `{"items":[{"url":"https://tides.example/tables","title":"His tables"}]}`)
	expect(t, "his import of the same page", status, body, 200, `"title":"His tables"`)
	h.settle()
	if h.count("documents WHERE user_id = '"+bob+"'") != 1 || h.count("notebook_sources s JOIN documents d ON d.id = s.document_id WHERE s.notebook_id = 20 AND d.user_id <> '"+bob+"'") != 0 {
		t.Error("his import linked a document that is not his")
	}
	if strings.Contains(body, `"title":"Tables"`) {
		t.Error("his source list shows her document")
	}

	// A pasted page: titled with its address until the page names itself.
	h.pages.byAddress["https://moon.example/phases"] = pagereader.Page{Status: pagereader.Read, Title: "Phases of the moon", Text: "# Phases of the moon\n\nNew, full."}
	status, body = h.as(alice, "POST", "/api/v1/notebooks/10/research/read-page", `{"url":" https://moon.example/phases "}`)
	// The answer lists it at once; its title there is the address or, if
	// the read has already finished, the page's own, so neither is asserted.
	expect(t, "her pasted page", status, body, 200, `"document_type":"CRAWLED_URL"`)
	h.settle()
	if h.count(`documents WHERE title = 'Phases of the moon' AND source_markdown = E'# Phases of the moon\n\nNew, full.' AND document_metadata ->> 'research_source' = 'web'`) != 1 {
		t.Error("the pasted page did not take the page's title, or its heading was doubled")
	}
	status, body = h.as(bob, "POST", "/api/v1/notebooks/10/research/read-page", `{"url":"https://moon.example/phases"}`)
	expect(t, "his page into her notebook", status, body, 404, notebookAbsent)
	for _, private := range []string{"http://localhost/admin", "http://169.254.169.254/latest/meta-data/", "http://10.0.0.5/"} {
		status, body = h.as(alice, "POST", "/api/v1/notebooks/10/research/read-page", `{"url":"`+private+`"}`)
		expect(t, private, status, body, 422, "isn't a public web page")
	}
	status, body = h.as(alice, "POST", "/api/v1/notebooks/10/research/read-page", `{"url":"ftp://moon.example/"}`)
	expect(t, "not a web address", status, body, 422, "url")
	if h.count("documents WHERE user_id = '"+alice+"'") != 4 {
		t.Error("a refused page made a document")
	}
}

// A read the server's stop cut short is marked, and importing the page
// again reads it; so is one left waiting by a process that died.
func TestAnImportWhoseReadNeverRanIsReadByImportingAgain(t *testing.T) {
	h := newHarness(t)
	const alice = testpostgres.Alice
	testpostgres.Exec(t, h.pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, unique_identifier_hash, document_metadata, status, updated_at) VALUES
 (500, '`+alice+`', 'Cut short', 'CRAWLED_URL', '', 'a', 'h1', '{"url":"https://cut.example/","research_source":"web"}', '{"state":"failed","reason":"x"}', '2026-11-12 08:59:00+00'),
 (501, '`+alice+`', 'Abandoned', 'CRAWLED_URL', '', 'b', 'h2', '{"url":"https://abandoned.example/","research_source":"web"}', '{"state":"processing","stage":"reading"}', '2026-11-12 08:00:00+00'),
 (502, '`+alice+`', 'Being read', 'CRAWLED_URL', '', 'c', 'h3', '{"url":"https://busy.example/","research_source":"web"}', '{"state":"processing","stage":"reading"}', '2026-11-12 08:59:30+00'),
 (503, '`+alice+`', 'An upload that failed', 'FILE', '', 'd', 'h4', '{"url":"https://upload.example/"}', '{"state":"failed","reason":"parser"}', '2026-11-12 08:00:00+00');`)
	status, body := h.as(alice, "POST", "/api/v1/notebooks/10/research/import", `{"items":[
 {"url":"https://cut.example/","title":"Cut short"},{"url":"https://abandoned.example/","title":"Abandoned"},
 {"url":"https://busy.example/","title":"Being read"},{"url":"https://upload.example/","title":"An upload"}]}`)
	expect(t, "the import", status, body, 200)
	h.settle()
	if h.count("documents WHERE id IN (500, 501) AND document_metadata ? 'fetched' AND status ->> 'state' = 'pending'") != 2 {
		t.Error("the two unread imports were not read")
	}
	// One still being read is left to its reader, and a document that is
	// not an import is never touched, whatever state it is in.
	if h.count(`documents WHERE id = 502 AND status ->> 'state' = 'processing' AND NOT document_metadata ? 'fetched'`) != 1 ||
		h.count(`documents WHERE id = 503 AND status ->> 'reason' = 'parser' AND source_markdown IS NULL`) != 1 {
		t.Error("a read under way was doubled, or a document that is not an import was rewritten")
	}
	if h.count("documents") != 4 || h.count("notebook_sources WHERE notebook_id = 10") != 4 {
		t.Error("the import duplicated a document it should have found")
	}
}

func TestAStoppedServerMarksItsUnfinishedReads(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice = testpostgres.Alice
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, document_metadata, status) VALUES
 (600, '`+alice+`', 'Waiting', 'CRAWLED_URL', '', 'a', '{"url":"https://w.example/","research_source":"web"}', '{"state":"pending","stage":"reading"}');`)
	lifetime, stop := context.WithCancel(context.Background())
	stop()
	queued := &indexing{}
	reads, err := NewReads(lifetime, 1, pool, &pages{}, &papers{}, queued, localOriginals(t), quiet())
	if err != nil {
		t.Fatal(err)
	}
	// The one slot is taken, so the read waits and sees the stop.
	reads.slots <- struct{}{}
	reads.Start(600, alice, "req")
	if err := reads.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	var reason string
	if err := pool.QueryRow(context.Background(), `SELECT status ->> 'reason' FROM documents WHERE id = 600 AND status ->> 'state' = 'failed'`).Scan(&reason); err != nil || reason != interrupted {
		t.Errorf("reason = %q, %v", reason, err)
	}
	if len(queued.queued) != 0 {
		t.Error("an unread document was queued for indexing")
	}
}

// The stream is server-sent events, flushed as they happen, with the
// headers a proxy needs.
func TestTheSearchStreamsEventsAsTheyHappen(t *testing.T) {
	h := newHarness(t)
	h.papers.hold = make(chan struct{})
	request, _ := http.NewRequest("POST", h.server.URL+"/api/v1/notebooks/10/research/search", strings.NewReader(`{"query":"tides","sources":["pubmed"]}`))
	request.Header.Set("X-Test-User", testpostgres.Alice)
	response, err := h.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("X-Accel-Buffering") != "no" || response.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("headers = %v", response.Header)
	}
	lines := bufio.NewReader(response.Body)
	// The first event arrives while PubMed is still being waited for.
	first, err := lines.ReadString('\n')
	if err != nil || first != "data: {\"type\":\"phase\",\"phase\":\"searching\",\"progress\":5}\n" {
		t.Fatalf("first line = %q, %v", first, err)
	}
	close(h.papers.hold)
	rest, _ := io.ReadAll(lines)
	if !strings.HasSuffix(string(rest), "data: {\"type\":\"done\"}\n\n") {
		t.Errorf("the stream ended %q", rest)
	}
}
