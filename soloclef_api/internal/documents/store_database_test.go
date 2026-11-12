package documents

import (
	"context"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres this test may empty
// (internal/testpostgres deletes every account in it). Unset, the test is
// skipped.
//
//	SOLOCLEF_API_TEST_DOCUMENTS_DATABASE=postgres://postgres:check@127.0.0.1:<port>/postgres \
//	    go test ./internal/documents/ -run RealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_DOCUMENTS_DATABASE"

// The statements against Postgres, with two accounts. The route tests use a
// stand-in for the database; that every statement names the owner is a
// property of the SQL, and is shown here: Alice reads her rows through
// every method of the store, and Bob, asking for the same ids, reads and
// changes nothing.
func TestTheStoreOverARealDatabaseKeepsTwoAccountsApart(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO folders (id, user_id, name, position, metadata) VALUES
 (7, '`+alice+`', 'Shore', 'a0', '{"watched": true}'),
 (8, '`+alice+`', 'Unwatched', 'a1', NULL);
INSERT INTO documents (id, user_id, title, document_type, document_metadata, content, content_hash, source_markdown, status, updated_at) VALUES
 (41, '`+alice+`', 'Tide tables', 'FILE', '{"pages": 2}', 'text', 'hash-41', '# Tides, now', '{"state": "ready"}', now()),
 (42, '`+alice+`', 'Shore notes', 'NOTE', '{}', 'text', 'hash-42', NULL, '{"state": "processing", "stage": "embedding"}', now() - interval '1 hour');
INSERT INTO chunks (id, document_id, user_id, content, "position") VALUES
 (501, 41, '`+alice+`', 'The spring tide peaks after the full moon.', 0),
 (502, 41, '`+alice+`', 'The neap tide follows the quarter moon.', 1),
 (503, 41, '`+alice+`', 'A limpet returns to its scar.', 2);
INSERT INTO document_versions (document_id, version_number, source_markdown, content_hash, title) VALUES
 (41, 1, '# Tides, before', 'hash-old', 'Tide tables, first draft');`)
	store := Store{Pool: pool}

	t.Run("the owner reads everything", func(t *testing.T) {
		if owned, err := store.Owns(ctx, 41, alice); err != nil || !owned {
			t.Fatalf("Owns = %v, %v", owned, err)
		}
		cited, err := store.CitedChunk(ctx, 502, alice)
		if err != nil || cited == nil || cited.DocumentID != 41 || cited.Position != 1 {
			t.Fatalf("CitedChunk = %+v, %v", cited, err)
		}
		record, err := store.RecordOf(ctx, 41, alice)
		if err != nil || record == nil || record.Title != "Tide tables" || record.DocumentType != "FILE" {
			t.Fatalf("RecordOf = %+v, %v", record, err)
		}
		if count, err := store.ChunkCount(ctx, 41, alice); err != nil || count != 3 {
			t.Errorf("ChunkCount = %d, %v", count, err)
		}
		if before, err := store.ChunksBefore(ctx, *cited, alice); err != nil || before != 1 {
			t.Errorf("ChunksBefore = %d, %v", before, err)
		}
		page, err := store.ChunkPage(ctx, 41, alice, 2, 1)
		if err != nil || len(page) != 2 || page[0].ID != 502 || page[1].ID != 503 {
			t.Errorf("ChunkPage = %+v, %v", page, err)
		}
		progress, err := store.ProgressOf(ctx, alice, []int64{42, 41, 9000})
		if err != nil || len(progress) != 2 || progress[0].ID != 41 || progress[1].ID != 42 {
			t.Errorf("ProgressOf = %+v, %v", progress, err)
		}
		counts, err := store.TypeCountsOf(ctx, alice)
		// In the order the enum declares its labels: FILE before NOTE.
		if err != nil || len(counts) != 2 || counts[0] != (TypeCount{"FILE", 1}) || counts[1] != (TypeCount{"NOTE", 1}) {
			t.Errorf("TypeCountsOf = %+v, %v", counts, err)
		}
		watched, err := store.WatchedFoldersOf(ctx, alice)
		if err != nil || len(watched) != 1 || watched[0].ID != 7 || watched[0].Name != "Shore" {
			t.Errorf("WatchedFoldersOf = %+v, %v", watched, err)
		}
		// Most recently updated first.
		recent, err := store.RecentTitles(ctx, alice, 10, 0)
		if err != nil || len(recent) != 2 || recent[0].ID != 41 || recent[1].ID != 42 {
			t.Errorf("RecentTitles = %+v, %v", recent, err)
		}
		// pg_trgm's similarity, which the migrations must have installed.
		matching, err := store.MatchingTitles(ctx, alice, "tide", 10, 0)
		if err != nil || len(matching) != 1 || matching[0].ID != 41 {
			t.Errorf("MatchingTitles = %+v, %v", matching, err)
		}
	})

	t.Run("another account reads none of it", func(t *testing.T) {
		if owned, err := store.Owns(ctx, 41, bob); err != nil || owned {
			t.Errorf("Owns = %v, %v", owned, err)
		}
		if cited, err := store.CitedChunk(ctx, 502, bob); err != nil || cited != nil {
			t.Errorf("CitedChunk = %+v, %v", cited, err)
		}
		if record, err := store.RecordOf(ctx, 41, bob); err != nil || record != nil {
			t.Errorf("RecordOf = %+v, %v", record, err)
		}
		if count, err := store.ChunkCount(ctx, 41, bob); err != nil || count != 0 {
			t.Errorf("ChunkCount = %d, %v", count, err)
		}
		if before, err := store.ChunksBefore(ctx, Cited{ID: 502, DocumentID: 41, Position: 1}, bob); err != nil || before != 0 {
			t.Errorf("ChunksBefore = %d, %v", before, err)
		}
		if page, err := store.ChunkPage(ctx, 41, bob, 20, 0); err != nil || len(page) != 0 {
			t.Errorf("ChunkPage = %+v, %v", page, err)
		}
		if restorable, err := store.Restorable(ctx, 41, bob); err != nil || restorable != nil {
			t.Errorf("Restorable = %+v, %v", restorable, err)
		}
		if progress, err := store.ProgressOf(ctx, bob, []int64{41, 42}); err != nil || len(progress) != 0 {
			t.Errorf("ProgressOf = %+v, %v", progress, err)
		}
		if counts, err := store.TypeCountsOf(ctx, bob); err != nil || len(counts) != 0 {
			t.Errorf("TypeCountsOf = %+v, %v", counts, err)
		}
		if watched, err := store.WatchedFoldersOf(ctx, bob); err != nil || len(watched) != 0 {
			t.Errorf("WatchedFoldersOf = %+v, %v", watched, err)
		}
		if recent, err := store.RecentTitles(ctx, bob, 10, 0); err != nil || len(recent) != 0 {
			t.Errorf("RecentTitles = %+v, %v", recent, err)
		}
		if matching, err := store.MatchingTitles(ctx, bob, "tide", 10, 0); err != nil || len(matching) != 0 {
			t.Errorf("MatchingTitles = %+v, %v", matching, err)
		}
	})

	// The restore's write names the owner too. A restore carrying another
	// account (which no route can build, since Restorable gives it none)
	// still adds its snapshot row inside the transaction, and the document
	// itself is not touched.
	t.Run("a restore under another account does not change the document", func(t *testing.T) {
		version, err := store.Version(ctx, 41, 1)
		if err != nil || version == nil {
			t.Fatalf("Version = %+v, %v", version, err)
		}
		forged := Restorable{ID: 41, UserID: bob, Title: "Tide tables", ContentHash: "hash-41"}
		if err := store.Restore(ctx, RestoreWork{Document: forged, Version: *version, Now: time.Now()}); err != nil {
			t.Fatal(err)
		}
		var markdown, title string
		if err := pool.QueryRow(ctx, `SELECT source_markdown, title FROM documents WHERE id = 41`).Scan(&markdown, &title); err != nil {
			t.Fatal(err)
		}
		if markdown != "# Tides, now" || title != "Tide tables" {
			t.Errorf("the document became %q, %q", title, markdown)
		}
		testpostgres.Exec(t, pool, `DELETE FROM document_versions WHERE document_id = 41 AND version_number > 1`)
	})

	t.Run("the owner's restore keeps the current text as a version and puts the old one back", func(t *testing.T) {
		restorable, err := store.Restorable(ctx, 41, alice)
		if err != nil || restorable == nil || restorable.UserID != alice {
			t.Fatalf("Restorable = %+v, %v", restorable, err)
		}
		version, err := store.Version(ctx, 41, 1)
		if err != nil || version == nil {
			t.Fatalf("Version = %+v, %v", version, err)
		}
		if err := store.Restore(ctx, RestoreWork{Document: *restorable, Version: *version, Now: time.Now()}); err != nil {
			t.Fatal(err)
		}
		var markdown, title string
		var reindex bool
		if err := pool.QueryRow(ctx, `SELECT source_markdown, title, content_needs_reindexing FROM documents WHERE id = 41`).
			Scan(&markdown, &title, &reindex); err != nil {
			t.Fatal(err)
		}
		if markdown != "# Tides, before" || title != "Tide tables, first draft" || !reindex {
			t.Errorf("the document is %q, %q, needs reindexing = %v", title, markdown, reindex)
		}
		versions, err := store.Versions(ctx, 41)
		if err != nil || len(versions) != 2 || versions[0].Number != 2 || versions[1].Number != 1 {
			t.Fatalf("Versions = %+v, %v", versions, err)
		}
		kept, err := store.Version(ctx, 41, 2)
		if err != nil || kept == nil || kept.SourceMarkdown == nil || *kept.SourceMarkdown != "# Tides, now" {
			t.Errorf("the snapshot = %+v, %v", kept, err)
		}
	})
}
