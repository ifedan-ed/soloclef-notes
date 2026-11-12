package notebooksearch

import (
	"reflect"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_NOTEBOOKSEARCH_DATABASE"

func TestASearchReadsOnlyTheCallersNotebooks(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title, icon, updated_at) VALUES
 (10, '`+alice+`', 'Lungs', '🫁', '2026-11-01'), (11, '`+alice+`', 'Empty', NULL, '2026-11-02'), (20, '`+bob+`', 'His', NULL, '2026-11-03');
INSERT INTO folders (id, user_id, name, position) VALUES (30, '`+alice+`', 'Papers', 'a0'), (31, '`+bob+`', 'His folder', 'a0');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, folder_id, status) VALUES
 (100, '`+alice+`', 'Guideline', 'FILE', '', 'a', 30, '{"state":"ready"}'),
 (101, '`+alice+`', 'In the folder', 'FILE', '', 'b', 30, '{"state":"ready"}'),
 (102, '`+alice+`', 'A deck', 'ARTIFACT', '', 'c', 30, '{"state":"ready"}'),
 (103, '`+alice+`', 'Going', 'FILE', '', 'd', 30, '{"state":"deleting"}'),
 (104, '`+alice+`', 'Unticked', 'FILE', '', 'e', NULL, '{"state":"ready"}'),
 (105, '`+alice+`', 'Her note', 'NOTE', '', 'f', NULL, '{"state":"ready"}'),
 (106, '`+alice+`', 'Unfiled note', 'NOTE', '', 'g', NULL, '{"state":"ready"}'),
 (107, '`+alice+`', 'In no notebook', 'FILE', '', 'h', NULL, '{"state":"ready"}'),
 (200, '`+bob+`', 'His paper', 'FILE', '', 'i', 31, '{"state":"ready"}'),
 (201, '`+bob+`', 'His note', 'NOTE', '', 'j', NULL, '{"state":"ready"}');
INSERT INTO notes (document_id, user_id, notebook_id) VALUES
 (105, '`+alice+`', 10), (106, '`+alice+`', NULL), (201, '`+bob+`', 20);
INSERT INTO notebook_sources (id, notebook_id, user_id, document_id, folder_id, enabled) VALUES
 (1, 10, '`+alice+`', 100, NULL, true), (2, 10, '`+alice+`', NULL, 30, true), (3, 10, '`+alice+`', 104, NULL, false),
 (4, 20, '`+bob+`', 200, NULL, true), (5, 20, '`+bob+`', NULL, 31, true);`)

	// Every document there is "matches": what is shown is then decided by
	// the scope alone.
	everything := &fixedSearch{}
	for id, title := range map[int64]string{100: "Guideline", 101: "In the folder", 102: "A deck", 103: "Going", 104: "Unticked",
		105: "Her note", 106: "Unfiled note", 107: "In no notebook", 200: "His paper", 201: "His note"} {
		everything.hits = append(everything.hits, hit(id, title, "the spacer"))
	}
	route := Route{Notebooks: New(pool), Search: everything}

	status, body := search(t, route, alice, "/api/v1/notebooks/search?q=spacer")
	if status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	// Her ticked source, the document beside it in the ticked folder (the
	// guideline is in that folder too and is reached by its own row), and
	// the note filed in the notebook. Not the artifact, the document being
	// deleted, the unticked source, the unfiled note, or anything of his.
	if got, want := everything.scopes[0], (documents.Scope{Limited: true, Limit: []int64{100, 101, 105}}); !reflect.DeepEqual(got, want) {
		t.Errorf("her scope is %+v, want %+v", got, want)
	}
	for _, piece := range []string{`"id":10,"title":"Lungs","icon":"🫁","updated_at":"2026-11-01T00:00:00Z"`,
		`"kind":"source","title":"Guideline"`, `"document_id":100,"note_id":null,"source_id":1,"folder_name":null`,
		`"document_id":101,"note_id":null,"source_id":2,"folder_name":"Papers"`,
		`"kind":"note","title":"Her note"`, `"document_id":null,"note_id":105,"source_id":null`} {
		if !strings.Contains(body, piece) {
			t.Errorf("her results lack %s:\n%s", piece, body)
		}
	}
	for _, his := range []string{"His", "Empty", "A deck", "Going", "Unticked", "Unfiled", "In no notebook"} {
		if strings.Contains(body, his) {
			t.Errorf("her results show %q:\n%s", his, body)
		}
	}

	status, body = search(t, route, bob, "/api/v1/notebooks/search?q=spacer")
	if status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	if got, want := everything.scopes[1], (documents.Scope{Limited: true, Limit: []int64{200, 201}}); !reflect.DeepEqual(got, want) {
		t.Errorf("his scope is %+v, want %+v", got, want)
	}
	if strings.Contains(body, "Lungs") || strings.Contains(body, "Guideline") || strings.Contains(body, "Her note") {
		t.Errorf("his results show hers:\n%s", body)
	}

	// A row that points across accounts cannot be written at all: the
	// schema's composite keys refuse it, so the owner predicates are a
	// second lock and not the only one.
	if _, err := pool.Exec(t.Context(), `INSERT INTO notebook_sources (notebook_id, user_id, document_id) VALUES (20, '`+bob+`', 100)`); err == nil {
		t.Error("a source row naming another account's document was accepted")
	}
}
