package citedsources

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_CITEDSOURCES_DATABASE"

// The statement names only the caller's passages, whoever's ids are asked,
// and leaves out a document being deleted.
func TestOnlyTheCallersPassagesAreNamed(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status) VALUES
 (1, '`+testpostgres.Alice+`', 'Alice guideline', 'FILE', 'x', 'h1', '{"state": "ready"}'),
 (2, '`+testpostgres.Alice+`', 'Alice going', 'FILE', 'x', 'h2', '{"state": "deleting"}'),
 (3, '`+testpostgres.Bob+`', 'Bob private', 'FILE', 'x', 'h3', '{"state": "ready"}');
INSERT INTO chunks (id, document_id, user_id, content, page_start) VALUES
 (10, 1, '`+testpostgres.Alice+`', 'a', 4),
 (11, 1, '`+testpostgres.Alice+`', 'b', NULL),
 (20, 2, '`+testpostgres.Alice+`', 'c', 1),
 (30, 3, '`+testpostgres.Bob+`', 'd', 9);`)

	rows, err := New(pool).CitedPassages(context.Background(), CitedPassagesParams{
		ChunkIds: []int64{30, 20, 11, 10, 999}, UserID: testpostgres.Alice})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ChunkID != 10 || rows[1].ChunkID != 11 {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].Title != "Alice guideline" || rows[0].PageStart == nil || *rows[0].PageStart != 4 || rows[1].PageStart != nil {
		t.Fatalf("rows %+v", rows)
	}
	bob, err := New(pool).CitedPassages(context.Background(), CitedPassagesParams{ChunkIds: []int64{10, 11}, UserID: testpostgres.Bob})
	if err != nil || len(bob) != 0 {
		t.Fatalf("Bob read %+v (%v)", bob, err)
	}
}
