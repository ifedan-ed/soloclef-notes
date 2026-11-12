package sourceguides

import (
	"context"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_SOURCEGUIDES_DATABASE"

func TestAGuideIsWrittenOncePerTextAndIsItsOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title) VALUES (10, '`+alice+`', 'Hers'), (20, '`+bob+`', 'His');
INSERT INTO folders (id, user_id, name, position) VALUES (30, '`+alice+`', 'Papers', 'a0');
INSERT INTO documents (id, user_id, title, document_type, content, source_markdown, content_hash, status) VALUES
 (100, '`+alice+`', 'Guideline', 'FILE', 'chunk text', '  # Asthma in adults  ', 'h1', '{"state":"ready"}'),
 (101, '`+alice+`', 'Going', 'FILE', 'text', NULL, 'h2', '{"state":"deleting"}'),
 (200, '`+bob+`', 'His paper', 'FILE', 'his text', NULL, 'h3', '{"state":"ready"}');
INSERT INTO notebook_sources (id, notebook_id, user_id, document_id, folder_id) VALUES
 (1, 10, '`+alice+`', 100, NULL), (2, 10, '`+alice+`', NULL, 30), (3, 10, '`+alice+`', 101, NULL),
 (4, 20, '`+bob+`', 200, NULL);`)

	stand := &scripted{reply: "First guide."}
	route := Route{Guides: New(pool), Models: stand}
	get := func(userID, path string) (int, string) { return serve(t, route, userID, path) }
	expect := func(name string, status int, body string, want int, says string) {
		t.Helper()
		if status != want || !strings.Contains(body, says) {
			t.Errorf("%s: %d %s, want %d saying %q", name, status, body, want, says)
		}
	}

	status, body := get(alice, "/api/v1/notebooks/10/sources/1/guide")
	expect("her guide, first open", status, body, 200, `{"document_id":100,"title":"Guideline","summary":"First guide.","status":"ready"}`)
	if shown := stand.requests[0].Messages[1].Content; shown != "# Asthma in adults" {
		t.Errorf("the model was shown %q, want the document's Markdown, trimmed", shown)
	}
	stand.reply = "Second guide."
	status, body = get(alice, "/api/v1/notebooks/10/sources/1/guide")
	expect("her guide, second open", status, body, 200, `"summary":"First guide."`)
	if len(stand.requests) != 1 {
		t.Errorf("the model was asked %d times for one text, want once", len(stand.requests))
	}

	// Another language is another guide; it replaces the row, which holds one.
	status, body = get(alice, "/api/v1/notebooks/10/sources/1/guide?locale=FR")
	expect("her guide in French", status, body, 200, `"summary":"Second guide."`)

	// A saved document is summarised again.
	testpostgres.Exec(t, pool, `UPDATE documents SET content_hash = 'h1b' WHERE id = 100`)
	stand.reply = "Third guide."
	status, body = get(alice, "/api/v1/notebooks/10/sources/1/guide?locale=fr")
	expect("her guide after a save", status, body, 200, `"summary":"Third guide."`)
	var key, summary string
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), min(content_hash), min(summary) FROM document_source_guides`).Scan(&count, &key, &summary); err != nil {
		t.Fatal(err)
	}
	if count != 1 || key != "h1b@fr" || summary != "Third guide." {
		t.Errorf("kept %d guides, the one under %q saying %q; want one, under h1b@fr", count, key, summary)
	}

	asked := len(stand.requests)
	for name, path := range map[string]string{
		"her source asked by him":           "/api/v1/notebooks/10/sources/1/guide",
		"her source through his notebook":   "/api/v1/notebooks/20/sources/1/guide",
		"his own notebook, her source's id": "/api/v1/notebooks/20/sources/2/guide",
	} {
		status, body = get(bob, path)
		expect(name, status, body, 404, sourceAbsent)
	}
	status, body = get(alice, "/api/v1/notebooks/10/sources/2/guide")
	expect("a folder source", status, body, 404, sourceAbsent)
	status, body = get(alice, "/api/v1/notebooks/10/sources/3/guide")
	expect("a document being deleted", status, body, 404, sourceAbsent)
	status, body = get(alice, "/api/v1/notebooks/10/sources/4/guide")
	expect("his source asked by her", status, body, 404, sourceAbsent)
	if len(stand.requests) != asked {
		t.Error("a refused request reached the model")
	}

	// The write names the owner too: he cannot keep a guide on her document.
	if rows, err := New(pool).SaveGuide(context.Background(), SaveGuideParams{
		CacheKey: "x", Summary: "His words", DocumentID: 100, UserID: bob}); err != nil || rows != 0 {
		t.Errorf("his save on her document wrote %d rows (%v), want none", rows, err)
	}
}
