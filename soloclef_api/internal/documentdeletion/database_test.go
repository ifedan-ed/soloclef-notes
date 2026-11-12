package documentdeletion

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres this test may empty
// (internal/testpostgres deletes every account in it). Unset, the test is
// skipped.
const databaseVariable = "SOLOCLEF_API_TEST_DELETION_DATABASE"

// The removal's statements against Postgres. Ownership is the route's one
// question (Owns), and it is the statement that keeps two accounts apart;
// the rest take the document's chunks and files in order, and leave another
// document's alone.
func TestTheRemovalOverARealDatabase(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash) VALUES
 (41, '`+alice+`', 'mine.pdf', 'FILE', '', 'hash-41'),
 (42, '`+alice+`', 'kept.pdf', 'FILE', '', 'hash-42'),
 (43, '`+bob+`', 'theirs.pdf', 'FILE', '', 'hash-43');
INSERT INTO chunks (id, document_id, user_id, content, "position") VALUES
 (501, 41, '`+alice+`', 'one', 0), (502, 41, '`+alice+`', 'two', 1),
 (601, 42, '`+alice+`', 'kept', 0), (701, 43, '`+bob+`', 'theirs', 0);
INSERT INTO document_files (document_id, user_id, storage_backend, storage_key, original_filename, size_bytes) VALUES
 (41, '`+alice+`', 's3', 'documents/a/41/original/mine.pdf', 'mine.pdf', 10);`)
	rows := PostgresRows{Pool: pool}

	for _, test := range []struct {
		document int64
		account  string
		owned    bool
	}{{41, alice, true}, {41, bob, false}, {43, alice, false}, {43, bob, true}, {9000, alice, false}} {
		if owned, err := rows.Owns(ctx, test.document, test.account); err != nil || owned != test.owned {
			t.Errorf("Owns(%d, %s) = %v, %v; want %v", test.document, test.account, owned, err, test.owned)
		}
	}

	if marked, err := rows.Mark(ctx, 41); err != nil || !marked {
		t.Fatalf("Mark = %v, %v", marked, err)
	}
	ids, err := rows.ChunkIDs(ctx, 41, 1000)
	if err != nil || len(ids) != 2 || ids[0] != 501 || ids[1] != 502 {
		t.Fatalf("ChunkIDs = %v, %v", ids, err)
	}
	// An id list that strays into other documents removes none of theirs:
	// the statement names the document.
	if err := rows.DeleteChunks(ctx, 41, []int64{501, 502, 601, 701}); err != nil {
		t.Fatal(err)
	}
	files, err := rows.Files(ctx, 41)
	if err != nil || len(files) != 1 || files[0] != (StoredFile{Backend: "s3", Key: "documents/a/41/original/mine.pdf"}) {
		t.Fatalf("Files = %+v, %v", files, err)
	}
	// Only a marked document is deleted: 42 was never marked.
	for _, id := range []int64{41, 42} {
		if err := rows.DeleteDocument(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	var documents, chunks, fileRows int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM documents), (SELECT count(*) FROM chunks), (SELECT count(*) FROM document_files)`).
		Scan(&documents, &chunks, &fileRows); err != nil {
		t.Fatal(err)
	}
	if documents != 2 || chunks != 2 || fileRows != 0 {
		t.Errorf("left %d documents, %d chunks and %d file rows; want 2 (the unmarked one and the other account's), 2 and 0", documents, chunks, fileRows)
	}
}
