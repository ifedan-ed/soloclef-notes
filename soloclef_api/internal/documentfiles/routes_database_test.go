package documentfiles

import (
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

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_DOCUMENTFILES_DATABASE"

// The uploaded file every test streams: made up here, and long enough to
// take ranges from.
const pdfBytes = "%PDF-1.7 0123456789 abcdefghij the end"

// TestADocumentsFilesAreItsOwnersAlone runs the three routes over a real
// Postgres and a real local file store behind a real listener, because the
// stream's write deadline is the connection's and a recorder has none.
func TestADocumentsFilesAreItsOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status) VALUES
 (41, '`+alice+`', 'Tide tables', 'FILE', 'x', 'h41', '{"state":"ready"}'),
 (42, '`+alice+`', 'Shore notes', 'NOTE', 'x', 'h42', '{"state":"ready"}'),
 (43, '`+alice+`', 'Lost', 'FILE', 'x', 'h43', '{"state":"ready"}'),
 (44, '`+alice+`', 'Going', 'FILE', 'x', 'h44', '{"state":"deleting"}'),
 (45, '`+alice+`', 'Elsewhere', 'FILE', 'x', 'h45', '{"state":"ready"}'),
 (51, '`+bob+`',   'His tides', 'FILE', 'x', 'h51', '{"state":"ready"}');
INSERT INTO document_files (id, document_id, user_id, kind, storage_backend, storage_key, original_filename, mime_type, size_bytes, checksum_sha256, created_at) VALUES
 (90, 41, '`+alice+`', 'ORIGINAL', 'local', 'documents/41/old.pdf', 'old.pdf', 'application/pdf', 3, 'c90', '2026-01-01'),
 (91, 41, '`+alice+`', 'ORIGINAL', 'local', 'documents/41/tides.pdf', 'Tide tables – été.pdf', 'application/octet-stream', 38, 'c91', '2026-01-02'),
 (92, 41, '`+alice+`', 'REDACTED', 'local', 'documents/41/redacted.html', 'redacted.html', 'text/html', 5, NULL, '2026-01-03'),
 (93, 43, '`+alice+`', 'ORIGINAL', 'local', 'documents/43/gone.pdf', 'gone.pdf', 'application/pdf', 9, 'c93', '2026-01-01'),
 (94, 44, '`+alice+`', 'ORIGINAL', 'local', 'documents/41/tides.pdf', 'going.pdf', 'application/pdf', 38, 'c94', '2026-01-01'),
 (95, 45, '`+alice+`', 'ORIGINAL', 's3', 'documents/45/far.pdf', 'far.pdf', 'application/pdf', 9, 'c95', '2026-01-01'),
 (96, 51, '`+bob+`',   'ORIGINAL', 'local', 'documents/51/his.pdf', 'his.pdf', 'application/pdf', 6, 'c96', '2026-01-01');`)

	directory := t.TempDir()
	put := func(key, content string) {
		path := filepath.Join(directory, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("documents/41/tides.pdf", pdfBytes)
	put("documents/41/redacted.html", "<p>x")
	put("documents/51/his.pdf", "secret")
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
	Routes{Pool: pool, Files: stores, StallTimeout: time.Minute, Errors: errorenvelope.Writer{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: request.Header.Get("X-Test-Caller"), Method: principal.MethodSession})))
	}))
	defer server.Close()
	ask := func(userID, path string, headers ...string) (*http.Response, string) {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Test-Caller", userID)
		for i := 0; i+1 < len(headers); i += 2 {
			request.Header.Set(headers[i], headers[i+1])
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response, string(body)
	}

	t.Run("the manifest names the newest original", func(t *testing.T) {
		response, body := ask(alice, "/api/v1/documents/41/view-manifest")
		if response.StatusCode != 200 || !strings.Contains(body, `"presentation":"original","file":{"file_id":91,"filename":"Tide tables – été.pdf","mime_type":"application/pdf","size_bytes":38,"content_url":"/api/v1/documents/41/files/91/content"}`) {
			t.Errorf("%d %s", response.StatusCode, body)
		}
		if response.Header.Get("Cache-Control") != noStore {
			t.Errorf("the manifest may be cached: %q", response.Header.Get("Cache-Control"))
		}
		// Following the manifest's own address reaches the bytes.
		response, body = ask(alice, "/api/v1/documents/41/files/91/content")
		if response.StatusCode != 200 || body != pdfBytes {
			t.Errorf("the manifest's content_url answered %d %q", response.StatusCode, body)
		}
	})

	t.Run("the download is the whole file, to save", func(t *testing.T) {
		response, body := ask(alice, "/api/v1/documents/41/download-original")
		if response.StatusCode != 200 || body != pdfBytes {
			t.Fatalf("%d %q", response.StatusCode, body)
		}
		want := map[string]string{
			"Content-Length": "38", "Accept-Ranges": "bytes", "ETag": `"c91"`, "Cache-Control": noStore,
			// Stored as bytes, sent as bytes: a download is not interpreted.
			"Content-Type":           "application/octet-stream",
			"X-Content-Type-Options": "nosniff",
			"Content-Disposition":    `attachment; filename="Tide tables  t.pdf"; filename*=UTF-8''Tide%20tables%20%E2%80%93%20%C3%A9t%C3%A9.pdf`,
		}
		for name, value := range want {
			if got := response.Header.Get(name); got != value {
				t.Errorf("%s is %q, want %q", name, got, value)
			}
		}
	})

	t.Run("a range of the download", func(t *testing.T) {
		ranges := []struct {
			name, asked  string
			status       int
			body, answer string
		}{
			{"the first eight bytes", "bytes=0-7", 206, "%PDF-1.7", "bytes 0-7/38"},
			{"from the middle to the end", "bytes=31-", 206, "the end", "bytes 31-37/38"},
			{"the last three", "bytes=-3", 206, "end", "bytes 35-37/38"},
			{"past the end", "bytes=99-", 416, "", "bytes */38"},
		}
		for _, test := range ranges {
			response, body := ask(alice, "/api/v1/documents/41/download-original", "Range", test.asked)
			if response.StatusCode != test.status || body != test.body || response.Header.Get("Content-Range") != test.answer {
				t.Errorf("%s: %d %q %q", test.name, response.StatusCode, body, response.Header.Get("Content-Range"))
			}
		}
		// A resumed download whose file has not changed gets its range; a
		// browser that already has the file gets a 304 and no bytes.
		if response, body := ask(alice, "/api/v1/documents/41/download-original", "Range", "bytes=0-3", "If-Range", `"c91"`); response.StatusCode != 206 || body != "%PDF" {
			t.Errorf("a resumed download: %d %q", response.StatusCode, body)
		}
		if response, body := ask(alice, "/api/v1/documents/41/files/91/content", "If-None-Match", `"c91"`); response.StatusCode != 304 || body != "" {
			t.Errorf("a file the browser has: %d %q", response.StatusCode, body)
		}
	})

	t.Run("a viewer is sent a PDF as a PDF, in the page, and a page of HTML never", func(t *testing.T) {
		response, _ := ask(alice, "/api/v1/documents/41/files/91/content", "Range", "bytes=0-3")
		if response.StatusCode != 206 || response.Header.Get("Content-Type") != "application/pdf" ||
			!strings.HasPrefix(response.Header.Get("Content-Disposition"), "inline;") || response.Header.Get("Cache-Control") != immutable {
			t.Errorf("%d %v", response.StatusCode, response.Header)
		}
		response, _ = ask(alice, "/api/v1/documents/41/files/92/content")
		if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment;") ||
			response.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("stored HTML was sent to be shown: %d %v", response.StatusCode, response.Header)
		}
	})

	refused := []struct {
		name, user, path string
		status           int
		says             string
	}{
		{"his manifest of hers", bob, "/api/v1/documents/41/view-manifest", 404, documentAbsent},
		{"his download of hers", bob, "/api/v1/documents/41/download-original", 404, documentAbsent},
		{"her file through his document", bob, "/api/v1/documents/51/files/91/content", 404, fileAbsent},
		{"his file through her document", alice, "/api/v1/documents/41/files/96/content", 404, fileAbsent},
		{"his file by its own address, asked by her", alice, "/api/v1/documents/51/files/96/content", 404, fileAbsent},
		{"a note has no original", alice, "/api/v1/documents/42/download-original", 404, originalAbsent},
		{"a row whose bytes are gone", alice, "/api/v1/documents/43/download-original", 404, originalAbsent},
		{"a document being deleted", alice, "/api/v1/documents/44/download-original", 404, documentAbsent},
		{"its manifest", alice, "/api/v1/documents/44/view-manifest", 404, documentAbsent},
		{"its file", alice, "/api/v1/documents/44/files/94/content", 404, fileAbsent},
		// The row names a backend this process has no settings for: a loud
		// failure, never a look in a store it does have.
		{"a file in a store this process cannot read", alice, "/api/v1/documents/45/download-original", 500, ""},
		{"an id that is not a number", alice, "/api/v1/documents/x/files/y/content", 422, "file_id"},
		{"an id wider than any id", alice, "/api/v1/documents/99999999999999999999/view-manifest", 404, documentAbsent},
	}
	for _, test := range refused {
		t.Run(test.name, func(t *testing.T) {
			response, body := ask(test.user, test.path)
			if response.StatusCode != test.status || !strings.Contains(body, test.says) || strings.Contains(body, "secret") || strings.Contains(body, "%PDF") {
				t.Errorf("%d %s", response.StatusCode, body)
			}
		})
	}
}

func TestTheRoutesRefuseToStartWithoutAStallTimeout(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("routes with no stall timeout were mounted")
		}
	}()
	Routes{}.Register(humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0")))
}
