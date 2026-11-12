package notebookfollowups

import (
	"context"
	"reflect"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_NOTEBOOKFOLLOWUPS_DATABASE"

func TestANotebooksTitlesAreItsOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title) VALUES (10, '`+alice+`', 'Hers'), (20, '`+bob+`', 'His');
INSERT INTO folders (id, user_id, name, position) VALUES (30, '`+alice+`', 'Papers', 'a0');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash) VALUES
 (100, '`+alice+`', 'Guideline', 'FILE', '', 'a'), (101, '`+alice+`', 'Unticked', 'FILE', '', 'b'),
 (200, '`+bob+`', 'His paper', 'FILE', '', 'c');
INSERT INTO notebook_sources (notebook_id, user_id, document_id, folder_id, enabled, added_at) VALUES
 (10, '`+alice+`', 100, NULL, true, '2026-01-02'), (10, '`+alice+`', 101, NULL, false, '2026-01-03'),
 (10, '`+alice+`', NULL, 30, true, '2026-01-01'), (20, '`+bob+`', 200, NULL, true, '2026-01-01');`)

	read := func(userID string, notebookID int64, limit int32) NotebookWithSourceTitlesRow {
		t.Helper()
		row, err := New(pool).NotebookWithSourceTitles(context.Background(),
			NotebookWithSourceTitlesParams{NotebookID: notebookID, UserID: userID, TitleLimit: limit})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	if got := read(alice, 10, 20); !got.Owned || !reflect.DeepEqual(got.Titles, []string{"Papers", "Guideline"}) {
		t.Errorf("her notebook: %+v, want her ticked sources in the order added", got)
	}
	if got := read(alice, 10, 1); !reflect.DeepEqual(got.Titles, []string{"Papers"}) {
		t.Errorf("the limit: %+v", got)
	}
	if got := read(bob, 10, 20); got.Owned || len(got.Titles) != 0 {
		t.Errorf("her notebook asked by him: %+v, want nothing", got)
	}
	if got := read(alice, 20, 20); got.Owned || len(got.Titles) != 0 {
		t.Errorf("his notebook asked by her: %+v, want nothing", got)
	}
}
