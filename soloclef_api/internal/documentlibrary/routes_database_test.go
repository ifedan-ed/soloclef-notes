package documentlibrary

import (
	"context"
	"errors"
	"fmt"
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
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_DOCUMENTLIBRARY_DATABASE"

// queued records the documents whose indexing was queued, and can be made
// to fail as a queue that does not answer does.
type queued struct {
	documents []int64
	down      bool
}

func (q *queued) Enqueue(_ context.Context, documentID int64, _ produce.LogContext) error {
	if q.down {
		return errors.New("the queue did not answer")
	}
	q.documents = append(q.documents, documentID)
	return nil
}

func TestTheLibraryIsItsOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// The text is made up for the test; nothing here is an account's own.
	testpostgres.Exec(t, pool, `
INSERT INTO folders (id, user_id, name, position) VALUES (7, '`+alice+`', 'Shore', 'a0'), (8, '`+bob+`', 'His', 'a0');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, source_markdown, folder_id, status, document_metadata, created_at) VALUES
 (41, '`+alice+`', 'Tide tables', 'FILE', 'High water at six.', 'h41', NULL, 7, '{"state":"ready"}', '{"url":"https://example.test/t"}', '2026-01-01'),
 (42, '`+alice+`', 'Shore notes', 'NOTE', 'Kelp line.', 'h42', '# Kelp line', NULL, '{"state":"ready"}', NULL, '2026-01-02'),
 (43, '`+alice+`', '100% sure', 'NOTE', 'x', 'h43', 'x', NULL, '{"state":"failed","reason":"No text"}', '[1]', '2026-01-03'),
 (44, '`+alice+`', 'Going', 'FILE', 'x', 'h44', NULL, NULL, '{"state":"deleting"}', NULL, '2026-01-04'),
 (45, '`+alice+`', 'Deck', 'ARTIFACT', 'x', 'h45', 'x', NULL, '{"state":"ready"}', NULL, '2026-01-05'),
 (46, '`+alice+`', 'Waiting', 'NOTE', 'old', 'h46', 'old', NULL, '{"state":"pending"}', NULL, '2026-01-06'),
 (51, '`+bob+`',   'His tides', 'FILE', 'Secret.', 'h51', 'Secret.', 8, '{"state":"ready"}', NULL, '2026-01-07');`)

	jobs := &queued{}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Pool: pool, Indexing: jobs, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }}.Register(api)
	ask := func(userID, method, path, body string, headers ...string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		for i := 0; i+1 < len(headers); i += 2 {
			request.Header.Set(headers[i], headers[i+1])
		}
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

	reads := []struct {
		name, user, path string
		status           int
		says, lacks      []string
	}{
		{"her library, newest first, without the one being deleted", alice, "/api/v1/documents", 200,
			[]string{`"items":[{"id":46,`, `"total":5`, `"has_more":false`, `"page":0`, `"page_size":50`,
				`"id":41,"title":"Tide tables","document_type":"FILE","document_metadata":{"url":"https://example.test/t"},"content":"","content_preview":"High water at six."`,
				`"folder_id":7,"status":{"state":"ready","reason":null}`,
				// A list where there should be an object does not fail the page.
				`"id":43,"title":"100% sure","document_type":"NOTE","document_metadata":{}`, `"status":{"state":"failed","reason":"No text"}`},
			[]string{"Going", "His tides", "Secret"}},
		{"his library is his", bob, "/api/v1/documents", 200, []string{`"id":51`, `"total":1`}, []string{"Tide", "Shore"}},
		{"a page of two", alice, "/api/v1/documents?page_size=2", 200, []string{`"items":[{"id":46,`, `"id":45,`, `"total":5`, `"has_more":true`}, []string{`"id":43`}},
		{"the last page", alice, "/api/v1/documents?page_size=2&page=2", 200, []string{`"items":[{"id":41,`, `"total":5`, `"has_more":false`, `"page":2`}, nil},
		{"a page past the end still knows the total", alice, "/api/v1/documents?page_size=2&page=9", 200, []string{`"items":[]`, `"total":5`, `"has_more":false`}, nil},
		{"only notes, by title", alice, "/api/v1/documents?document_types=NOTE&sort_by=title&sort_order=asc", 200,
			[]string{`"items":[{"id":43,`, `"total":3`}, []string{`"id":41`, `"id":45`}},
		{"one folder", alice, "/api/v1/documents?folder_id=7", 200, []string{`"id":41`, `"total":1`}, nil},
		{"his folder, asked by her, holds nothing of hers", alice, "/api/v1/documents?folder_id=8", 200, []string{`"items":[]`, `"total":0`}, []string{"His"}},
		{"no folder", alice, "/api/v1/documents?folder_id=root", 200, []string{`"total":4`}, []string{`"id":41`}},
		{"a type that does not exist", alice, "/api/v1/documents?document_types=MEMO", 422, []string{"document_types"}, nil},
		{"the title search, in id order", alice, "/api/v1/documents/search?title=tide", 200, []string{`"items":[{"id":41,`, `"total":1`}, []string{"His"}},
		{"a percent sign is a percent sign", alice, "/api/v1/documents/search?title=%25", 200, []string{`"id":43`, `"total":1`}, nil},
		{"a search with no term", alice, "/api/v1/documents/search", 422, []string{"title"}, nil},
		{"one document, with its text", alice, "/api/v1/documents/42", 200,
			[]string{`"id":42,"title":"Shore notes","document_type":"NOTE","document_metadata":{},"content":"# Kelp line","content_preview":"# Kelp line"`}, nil},
		{"a file's text is what was indexed", alice, "/api/v1/documents/41", 200, []string{`"content":"High water at six."`}, nil},
		{"his document, asked by her", alice, "/api/v1/documents/51", 404, []string{"Document with id 51 not found"}, []string{"Secret"}},
		{"hers, asked by him", bob, "/api/v1/documents/42", 404, []string{"Document with id 42 not found"}, []string{"Kelp"}},
		{"one being deleted", alice, "/api/v1/documents/44", 404, []string{"not found"}, nil},
		{"an id that is not a number", alice, "/api/v1/documents/forty", 422, []string{"document_id"}, nil},
		{"an id wider than any id", alice, "/api/v1/documents/99999999999999999999", 404, []string{"not found"}, nil},
	}
	for _, test := range reads {
		t.Run(test.name, func(t *testing.T) {
			status, body := ask(test.user, "GET", test.path, "")
			if status != test.status {
				t.Fatalf("%d %s", status, body)
			}
			for _, piece := range test.says {
				if !strings.Contains(body, piece) {
					t.Errorf("%s lacks %s", body, piece)
				}
			}
			for _, piece := range test.lacks {
				if strings.Contains(body, piece) {
					t.Errorf("%s should not say %s", body, piece)
				}
			}
		})
	}

	t.Run("the concept, for a caller that asks for Markdown", func(t *testing.T) {
		status, body := ask(alice, "GET", "/api/v1/documents/41", "", "Accept", "text/markdown")
		if status != 200 || !strings.HasPrefix(body, "---\ntype: \"File\"\nresource: \"https://example.test/t\"\ntitle: \"Tide tables\"\n") ||
			!strings.HasSuffix(body, "---\n\nHigh water at six.\n") {
			t.Errorf("%d %s", status, body)
		}
		if status, body := ask(bob, "GET", "/api/v1/documents/41", "", "Accept", "text/markdown"); status != 404 || strings.Contains(body, "High water") {
			t.Errorf("his read of her concept: %d %s", status, body)
		}
	})

	t.Run("adding a note", func(t *testing.T) {
		status, body := ask(alice, "POST", "/api/v1/documents",
			`{"title":" Neap  tides ","content":"Weak tides at the quarter moon.","source_url":"https://example.test/neap","folder_id":7}`)
		if status != 201 || !strings.Contains(body, `"title":"Neap tides","document_type":"NOTE","document_metadata":{"NOTE":true,"source_url":"https://example.test/neap"},"content":"Weak tides at the quarter moon."`) ||
			!strings.Contains(body, `"folder_id":7,"status":{"state":"pending","reason":null}`) {
			t.Fatalf("%d %s", status, body)
		}
		if count(`notes n JOIN documents d ON d.id = n.document_id WHERE d.title = 'Neap tides' AND n.user_id = '`+alice+`' AND n.origin = 'hand' AND d.content_needs_reindexing`) != 1 {
			t.Error("the note has no notes row, or is another account's")
		}
		if len(jobs.documents) != 1 {
			t.Fatalf("indexing was queued for %v, want the one new note", jobs.documents)
		}
		// It is at once a document of her library and nobody else's.
		if status, body := ask(bob, "GET", fmt.Sprintf("/api/v1/documents/%d", jobs.documents[0]), ""); status != 404 {
			t.Errorf("his read of her new note: %d %s", status, body)
		}

		refused := []struct {
			name, body string
			status     int
			says       string
		}{
			{"into his folder", `{"title":"T","content":"x","folder_id":8}`, 404, folderAbsent},
			{"with no text", `{"title":"T"}`, 422, "content"},
			{"in the extension's envelope", `{"document_type":"EXTENSION","content":[{"pageContent":"x"}]}`, 422, "body"},
			{"with a field it does not take", `{"title":"T","content":"x","owner":"bob"}`, 422, "owner"},
			{"that is not JSON", `title=T`, 422, "body"},
		}
		before := count("documents")
		for _, test := range refused {
			if status, body := ask(alice, "POST", "/api/v1/documents", test.body); status != test.status || !strings.Contains(body, test.says) {
				t.Errorf("%s: %d %s", test.name, status, body)
			}
		}
		if count("documents") != before || len(jobs.documents) != 1 {
			t.Error("a refused add wrote a row or queued a job")
		}
	})

	t.Run("saving", func(t *testing.T) {
		jobs.documents = nil
		status, body := ask(alice, "POST", "/api/v1/documents/42/save", `{"source_markdown":"# Kelp line\n\nAnd wrack."}`)
		if status != 200 || !strings.Contains(body, `"status":"saved","document_id":42,"message":"`+savedMessage+`","updated_at":"2026-11-12T09:00:00Z"`) {
			t.Fatalf("%d %s", status, body)
		}
		if count(`documents WHERE id = 42 AND title = 'Shore notes' AND source_markdown LIKE '%wrack.' AND status ->> 'state' = 'pending' AND content_needs_reindexing AND content_hash <> 'h42'`) != 1 {
			t.Error("the save did not write the text, keep the title, or mark the document for indexing")
		}
		if count(`document_versions WHERE document_id = 42 AND version_number = 1 AND source_markdown = '# Kelp line' AND title = 'Shore notes' AND content_hash = 'h42'`) != 1 {
			t.Error("the text that was replaced was not kept as a version")
		}
		if len(jobs.documents) != 1 || jobs.documents[0] != 42 {
			t.Errorf("queued %v, want the saved document", jobs.documents)
		}

		// The same text again: nothing to keep, write or index.
		status, body = ask(alice, "POST", "/api/v1/documents/42/save", `{"source_markdown":"# Kelp line\n\nAnd wrack."}`)
		if status != 200 || !strings.Contains(body, unchangedMessage) || count("document_versions WHERE document_id = 42") != 1 || len(jobs.documents) != 1 {
			t.Errorf("a save of the same text: %d %s", status, body)
		}

		// A new title alone is kept as a version and needs no indexing; a
		// document already waiting for its job is not queued twice.
		status, body = ask(alice, "POST", "/api/v1/documents/46/save", `{"source_markdown":"new","title":"Waited"}`)
		if status != 200 || count(`documents WHERE id = 46 AND title = 'Waited' AND source_markdown = 'new'`) != 1 || len(jobs.documents) != 1 {
			t.Errorf("a save of a waiting document: %d %s, queued %v", status, body, jobs.documents)
		}

		refused := []struct {
			name, user, path, body string
			status                 int
			says                   string
		}{
			{"his save of hers", bob, "/api/v1/documents/42/save", `{"source_markdown":"mine"}`, 404, "not found"},
			{"an artifact", alice, "/api/v1/documents/45/save", `{"source_markdown":"y"}`, 409, artifactReadOnly},
			{"one being deleted", alice, "/api/v1/documents/44/save", `{"source_markdown":"y"}`, 404, "not found"},
			{"with no text", alice, "/api/v1/documents/42/save", `{"title":"T"}`, 422, "source_markdown"},
			{"with a blank title", alice, "/api/v1/documents/42/save", `{"source_markdown":"y","title":"  "}`, 422, "title"},
			{"with the add's field", alice, "/api/v1/documents/42/save", `{"content":"y"}`, 422, "content"},
		}
		for _, test := range refused {
			if status, body := ask(test.user, "POST", test.path, test.body); status != test.status || !strings.Contains(body, test.says) {
				t.Errorf("%s: %d %s", test.name, status, body)
			}
		}
		if count(`documents WHERE id = 42 AND source_markdown LIKE '%wrack.'`) != 1 || count(`documents WHERE id IN (44, 45) AND source_markdown IS DISTINCT FROM 'y'`) != 2 {
			t.Error("a refused save wrote")
		}

		// A queue that does not answer does not lose the text.
		jobs.down = true
		status, body = ask(alice, "POST", "/api/v1/documents/42/save", `{"source_markdown":"third"}`)
		if status != 200 || count(`documents WHERE id = 42 AND source_markdown = 'third'`) != 1 || count("document_versions WHERE document_id = 42") != 2 {
			t.Errorf("a save while the queue is down: %d %s", status, body)
		}
	})
}
