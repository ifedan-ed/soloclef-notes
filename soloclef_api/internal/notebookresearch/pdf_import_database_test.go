package notebookresearch

// The import of a PDF link end to end, against throwaway services: the real
// page reader fetching from a site on loopback, a real Postgres and a real
// MinIO, and, when it is named, the real parse service reading the stored
// file the way the indexing worker reads it.
//
// Opt-in. Start the containers on loopback ports Docker picks, run, remove:
//
//	docker run -d --name nbr-pg -p 127.0.0.1::5432 -e POSTGRES_PASSWORD=t --tmpfs /var/lib/postgresql/data pgvector/pgvector:pg17
//	docker run -d --name nbr-minio -p 127.0.0.1::9000 -e MINIO_ROOT_USER=example-access \
//	    -e MINIO_ROOT_PASSWORD=example-secret --tmpfs /data minio/minio server /data
//	docker build -t soloclef-parse-service:test soloclef_parse_service
//	docker run -d --name nbr-parse -p 127.0.0.1::8080 -e PARSE_SERVICE_LISTEN=0.0.0.0:8080 -e PARSE_SERVICE_WORKERS=1 \
//	    -e PARSE_SERVICE_MAX_BYTES=10485760 -e PARSE_SERVICE_PARSE_TIMEOUT_SECONDS=60 -e PARSE_SERVICE_QUEUE_WAIT_SECONDS=30 \
//	    soloclef-parse-service:test
//
//	SOLOCLEF_API_TEST_NOTEBOOKRESEARCH_DATABASE=postgres://postgres:t@127.0.0.1:<port>/postgres \
//	SOLOCLEF_TEST_S3_ENDPOINT=http://127.0.0.1:<port> SOLOCLEF_TEST_S3_ACCESS_KEY=example-access \
//	SOLOCLEF_TEST_S3_SECRET_KEY=example-secret SOLOCLEF_API_TEST_PARSE_SERVICE_URL=http://127.0.0.1:<port> \
//	    go test ./internal/notebookresearch -run PDF -count=1
//
//	docker rm -f nbr-pg nbr-minio nbr-parse

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parserconfig"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
)

const (
	s3EndpointVariable   = "SOLOCLEF_TEST_S3_ENDPOINT"
	s3AccessVariable     = "SOLOCLEF_TEST_S3_ACCESS_KEY"
	s3SecretVariable     = "SOLOCLEF_TEST_S3_SECRET_KEY"
	parseServiceVariable = "SOLOCLEF_API_TEST_PARSE_SERVICE_URL"
)

func needVariable(t *testing.T, variable string) string {
	t.Helper()
	value := os.Getenv(variable)
	if value == "" {
		t.Skipf("%s is not set; this test needs throwaway services (see the top of this file)", variable)
	}
	return value
}

// testPDF is a real PDF with a text layer, one page per entry: one line of
// Helvetica each, plain ASCII. Built here so that no document is checked in.
func testPDF(pages ...string) []byte {
	var pdf bytes.Buffer
	var offsets []int
	object := func(body string) {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	pdf.WriteString("%PDF-1.4\n")
	object("<< /Type /Catalog /Pages 2 0 R >>")
	kids := ""
	for index := range pages {
		kids += fmt.Sprintf("%d 0 R ", 4+2*index)
	}
	object(fmt.Sprintf("<< /Type /Pages /Kids [ %s] /Count %d >>", kids, len(pages)))
	object("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for index, text := range pages {
		object(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
			"/Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*index))
		stream := fmt.Sprintf("BT /F1 14 Tf 72 720 Td (%s) Tj ET", text)
		object(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return pdf.Bytes()
}

// bucket is a MinIO bucket of this run's own, removed with what it holds.
func bucket(t *testing.T) (*filestore.S3, *minio.Client, string) {
	t.Helper()
	endpoint, access, secret := needVariable(t, s3EndpointVariable), needVariable(t, s3AccessVariable), needVariable(t, s3SecretVariable)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := minio.New(parsed.Host, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: parsed.Scheme == "https"})
	if err != nil {
		t.Fatal(err)
	}
	var raw [6]byte
	_, _ = rand.Read(raw[:])
	name := "research-pdf-" + hex.EncodeToString(raw[:])
	ctx := context.Background()
	if err := admin.MakeBucket(ctx, name, minio.MakeBucketOptions{Region: "us-east-1"}); err != nil {
		t.Fatalf("making the bucket: %v", err)
	}
	t.Cleanup(func() {
		for object := range admin.ListObjects(ctx, name, minio.ListObjectsOptions{Recursive: true}) {
			_ = admin.RemoveObject(ctx, name, object.Key, minio.RemoveObjectOptions{})
		}
		_ = admin.RemoveBucket(ctx, name)
	})
	store, err := filestore.OpenS3(filestore.S3Settings{
		Endpoint: endpoint, Bucket: name, Region: "us-east-1", PathStyle: true, AccessKey: access, SecretKey: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, admin, name
}

// privatePDFServer is a server on 127.0.0.2, which the reader's guard in
// this test refuses, serving a PDF. It counts the requests that reach it.
func privatePDFServer(t *testing.T, pdf []byte) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("this machine cannot listen on 127.0.0.2: %v", err)
	}
	hits := new(atomic.Int32)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdf)
	}))
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return server.URL, hits
}

// unusedPDFText is the chat's PDF reader where the test reads none.
type unusedPDFText struct{}

func (unusedPDFText) Read(context.Context, fileread.File, fileread.Choices) (fileread.Text, error) {
	return fileread.Text{}, &fileread.Unreadable{Reason: "no PDF is read for a chat here"}
}

func readerFor(t *testing.T, pdfs pagereader.PDFText) *pagereader.Reader {
	t.Helper()
	// The site is on loopback, so this guard lets 127.0.0.1 through and
	// nothing else: 127.0.0.2 stands for the private network.
	home := netip.MustParseAddr("127.0.0.1")
	reader, err := pagereader.New(pagereader.Settings{
		MaxPages: 5, MaxBytes: 1 << 20, Timeout: 10 * time.Second, MaxChars: 60_000, MaxRedirects: 3,
		UserAgent: "notebookresearch-test/1.0", Allow: func(a netip.Addr) bool { return a.Unmap() == home },
		MaxPDFBytes: fileread.MaxFileBytes, PDFTimeout: 30 * time.Second, PDFs: pdfs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func TestAPDFLinkIsImportedAsAnUploadedPDFIs(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	store, admin, bucketName := bucket(t)
	const alice = testpostgres.Alice
	testpostgres.Exec(t, pool, `INSERT INTO notebooks (id, user_id, title, updated_at) VALUES (10, '`+alice+`', 'Tides', '2026-01-01');`)

	tides := testPDF("The first page is about tides.", "The second page is about the moon.")
	phases := testPDF("Phases of the moon.")
	huge := append(testPDF("A large file."), bytes.Repeat([]byte("%"), 80<<10)...)
	secret, hits := privatePDFServer(t, testPDF("SECRET internal report"))
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/papers/tides":
			// A PDF the server calls a web page.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(tides)
		case "/download":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="Moon phases.pdf"`)
			_, _ = w.Write(phases)
		case "/wall.pdf":
			// A "PDF" that is a sign-in page.
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = io.WriteString(w, "<html><head><title>Sign in</title></head><body><p>Please sign in to read this paper.</p></body></html>")
		case "/huge.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write(huge)
		case "/leak.pdf":
			http.Redirect(w, r, secret+"/report.pdf", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(site.Close)

	queued := &indexing{}
	reader := readerFor(t, unusedPDFText{})
	reads, err := NewReads(context.Background(), 2, pool, reader, &papers{}, queued, Originals{Files: store, MaxBytes: 64 << 10}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	routes := Routes{
		Pool: pool, Papers: &papers{}, Web: web{}, Pages: reader, Reads: reads, Stall: 5 * time.Second,
		Parsers: parserchoicetest.Registry(t, parse.Local, parse.Datalab),
		Errors:  errorenvelope.Writer{}, Logger: quiet(),
		Now: func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) },
	}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.Adapter().ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(),
			principal.Principal{UserID: alice, Method: principal.MethodSession})))
	}))
	t.Cleanup(server.Close)
	post := func(path, body string) (int, string) {
		t.Helper()
		response, err := server.Client().Post(server.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(raw)
	}
	settle := func() {
		t.Helper()
		ctx, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		if err := reads.Stop(ctx); err != nil {
			t.Fatal(err)
		}
	}
	count := func(query string) (n int) {
		t.Helper()
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	objects := func() (keys []string) {
		for object := range admin.ListObjects(context.Background(), bucketName, minio.ListObjectsOptions{Recursive: true}) {
			keys = append(keys, object.Key)
		}
		return keys
	}

	// A parser that is not one, or is not configured, is refused by name
	// before anything is written.
	status, body := post("/api/v1/notebooks/10/research/import", `{"parser":"tesseract","items":[{"url":"`+site.URL+`/download","title":"Phases"}]}`)
	expect(t, "an unknown parser", status, body, 422, `is not a parser`)
	status, body = post("/api/v1/notebooks/10/research/import", `{"parser":"llamaparse","items":[{"url":"`+site.URL+`/download","title":"Phases"}]}`)
	expect(t, "a parser not configured", status, body, 422, "not configured on this server")
	if count("documents") != 0 {
		t.Fatal("a refused import wrote a document")
	}

	// Four links, no parser named; then one with Datalab named.
	status, body = post("/api/v1/notebooks/10/research/import", `{"items":[
 {"url":"`+site.URL+`/papers/tides","title":"Tides","snippet":"A teaser.","source":"exa"},
 {"url":"`+site.URL+`/wall.pdf","title":"Walled"},
 {"url":"`+site.URL+`/huge.pdf","title":"Huge","snippet":"What the search said."},
 {"url":"`+site.URL+`/leak.pdf","title":"Leak"}]}`)
	expect(t, "the import", status, body, 200, `"document_type":"CRAWLED_URL"`)
	status, body = post("/api/v1/notebooks/10/research/import", `{"parser":"datalab","items":[{"url":"`+site.URL+`/download","title":"Phases"}]}`)
	expect(t, "the import naming a parser", status, body, 200)
	settle()

	// Each PDF is its document's original, in MinIO, byte for byte, with
	// the row an upload makes; the document waits for the indexer with the
	// parser the import named, else the local one.
	for _, c := range []struct {
		title, name, parser string
		pdf                 []byte
	}{
		{"Tides", "tides.pdf", "local", tides},
		{"Phases", "Moon phases.pdf", "datalab", phases},
	} {
		sum := sha256.Sum256(c.pdf)
		var id int64
		var key, backend string
		err := pool.QueryRow(context.Background(), `
SELECT d.id, f.storage_key, f.storage_backend FROM documents d JOIN document_files f ON f.document_id = d.id AND f.user_id = d.user_id
WHERE d.user_id = $1 AND d.title = $2 AND d.document_type = 'CRAWLED_URL' AND d.parser = $3
  AND d.status ->> 'state' = 'pending' AND NOT d.status ? 'stage' AND NOT d.content_needs_reindexing
  AND d.content_hash = $4 AND d.document_metadata ->> 'fetched' = 'true' AND d.document_metadata ->> 'read_from' = 'pdf'
  AND d.document_metadata ->> 'FILE_NAME' = $5 AND (d.document_metadata ->> 'file_size')::bigint = $6
  AND f.kind = 'ORIGINAL' AND f.mime_type = 'application/pdf' AND f.original_filename = $5
  AND f.size_bytes = $6 AND f.checksum_sha256 = $4`,
			alice, c.title, c.parser, hex.EncodeToString(sum[:]), c.name, len(c.pdf)).Scan(&id, &key, &backend)
		if err != nil {
			t.Fatalf("%s: no document with its PDF as its original: %v", c.title, err)
		}
		if backend != store.Name() || !strings.HasPrefix(key, "documents/"+alice+"/"+strconv.FormatInt(id, 10)+"/original/") || !strings.HasSuffix(key, ".pdf") {
			t.Errorf("%s is stored at %s %q", c.title, backend, key)
		}
		stored, err := store.Open(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(stored)
		stored.Close()
		if !bytes.Equal(got, c.pdf) {
			t.Errorf("%s: MinIO holds %d bytes, want the %d of the PDF", c.title, len(got), len(c.pdf))
		}
	}
	// The "PDF" that was a sign-in page was read as the page it is; the PDF
	// over the limit, and the one behind a redirect into the private
	// network, were not kept, and say why.
	if count(`documents WHERE title = 'Walled' AND source_markdown LIKE '%Please sign in%' AND document_metadata ->> 'read_from' = 'page'`) != 1 {
		t.Error("the sign-in page called a PDF was not read as a page")
	}
	if count(`documents WHERE title = 'Huge' AND document_metadata ->> 'fetched' = 'false'
		AND document_metadata ->> 'fetch_problem' = 'The PDF is larger than 64.0 KB, the most that is kept for one file.'
		AND source_markdown LIKE '%What the search said.%'`) != 1 {
		t.Error("the PDF over the limit does not say why it was not kept")
	}
	if count(`documents WHERE title = 'Leak' AND document_metadata ->> 'fetched' = 'false'
		AND document_metadata ->> 'fetch_problem' LIKE 'This address is not on the public internet%'`) != 1 || hits.Load() != 0 {
		t.Errorf("the redirect into the private network was followed: %d requests reached it", hits.Load())
	}
	if count("document_files") != 2 || len(objects()) != 2 {
		t.Errorf("%d file rows and objects %v; want the two PDFs and nothing else", count("document_files"), objects())
	}
	if len(queued.queued) != 5 {
		t.Errorf("indexing was queued for %v, want all five documents", queued.queued)
	}

	// The same link again, spelled differently, is the same document: no
	// second document, file or download.
	status, body = post("/api/v1/notebooks/10/research/import", `{"items":[{"url":"`+strings.Replace(site.URL, "http://", "HTTP://", 1)+`/papers/tides/?utm_source=mail","title":"Tides again"}]}`)
	expect(t, "the same link again", status, body, 200)
	settle()
	if count("documents") != 5 || count("document_files") != 2 || len(objects()) != 2 || len(queued.queued) != 5 {
		t.Error("importing the same PDF link again made a second copy")
	}

	// The parse service reads the stored file the way the indexing worker
	// does: the row's newest ORIGINAL, from the store the row names, with
	// the row's parser.
	parseURL := os.Getenv(parseServiceVariable)
	if parseURL == "" {
		t.Logf("%s is not set: the stored PDF is not parsed here", parseServiceVariable)
		return
	}
	registry, err := parserconfig.Load(func(name string) string {
		return map[string]string{
			parserconfig.DefaultParser: "local", parserconfig.MaxTextBytes: "1048576", parserconfig.MaxMarkdownBytes: "8388608",
			parserconfig.LocalURL: parseURL, parserconfig.LocalTimeoutSeconds: "120",
		}[name]
	})
	if err != nil {
		t.Fatal(err)
	}
	stores, err := filestore.NewStores(store)
	if err != nil {
		t.Fatal(err)
	}
	var parser, backend, key, name string
	var size int64
	if err := pool.QueryRow(context.Background(), `
SELECT d.parser, f.storage_backend, f.storage_key, f.original_filename, f.size_bytes
FROM documents d JOIN LATERAL (
    SELECT * FROM document_files WHERE document_id = d.id AND kind = 'ORIGINAL' ORDER BY id DESC LIMIT 1) f ON true
WHERE d.title = 'Tides'`).Scan(&parser, &backend, &key, &name, &size); err != nil {
		t.Fatal(err)
	}
	chosen, err := selection.Of(parser)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := registry.Parse(context.Background(), chosen, parse.Source{Name: name, Size: size, Open: func(ctx context.Context) (io.ReadCloser, error) {
		named, err := stores.Named(backend)
		if err != nil {
			return nil, err
		}
		return named.Open(ctx, key)
	}})
	if err != nil {
		t.Fatalf("the parse service could not read the stored PDF: %v", err)
	}
	if parsed.Parser != parse.Local || len(parsed.Pages) != 2 ||
		!strings.Contains(parsed.PageMarkdown(0), "The first page is about tides.") || !strings.Contains(parsed.PageMarkdown(1), "The second page is about the moon.") {
		t.Errorf("parsed by %s into %d pages: %q", parsed.Parser, len(parsed.Pages), parsed.Markdown)
	}

	// And the chat reads the same link's text through the same service,
	// for its answer, adding no document.
	files, err := fileread.New(registry, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	pages := readerFor(t, files).Read(context.Background(), []string{site.URL + "/papers/tides"})
	if pages[0].Status != pagereader.Read || !pages[0].PDF || !strings.Contains(pages[0].Text, "The second page is about the moon.") {
		t.Errorf("the chat's read: %+v", pages[0])
	}
	if count("documents") != 5 || len(objects()) != 2 {
		t.Error("the chat's read of a PDF stored something")
	}
}
