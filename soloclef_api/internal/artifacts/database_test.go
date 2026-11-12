package artifacts

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// databaseVariable names a throwaway Postgres for this package's database
// tests. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the tests are
// skipped.
const databaseVariable = "SOLOCLEF_API_TEST_ARTIFACTS_DATABASE"

// TestTheStatementsOverARealDatabase asks the generated statements what the
// in-memory tests ask a stand-in: that the list is in its order and is its
// owner's alone, and that the delete's marker compares and writes the jsonb
// status the way the route relies on.
func TestTheStatementsOverARealDatabase(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// Alice has three artifacts, two of them tied on updated_at and one of
	// those made in a chat; Bob has one, made in a chat of his own.
	testpostgres.Exec(t, pool, `
INSERT INTO new_chat_threads (id, user_id, title) VALUES
 (1, '`+alice+`', 'A chat'), (2, '`+bob+`', 'His chat');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status) VALUES
 (500, '`+alice+`', 'A report', 'ARTIFACT', 'text', 'hash-500', '{"state": "ready"}'),
 (501, '`+alice+`', 'A deck',   'ARTIFACT', 'text', 'hash-501', '{"state": "ready"}'),
 (502, '`+alice+`', 'A film',   'ARTIFACT', 'text', 'hash-502', '{"state": "ready"}'),
 (505, '`+bob+`',   'His film', 'ARTIFACT', 'text', 'hash-505', '{"state": "ready"}');
INSERT INTO artifacts (id, document_id, user_id, thread_id, format, updated_at) VALUES
 (500, 500, '`+alice+`', 1,    'markdown',   '2026-06-03 00:00:00+00'),
 (501, 501, '`+alice+`', NULL, 'flashcards', '2026-06-02 00:00:00+00'),
 (502, 502, '`+alice+`', NULL, 'video',      '2026-06-02 00:00:00+00'),
 (505, 505, '`+bob+`',   2,    'video',      '2026-06-04 00:00:00+00');`)
	store := Store{Pool: pool}
	statusOf := func(documentID int64) string {
		var status string
		if err := pool.QueryRow(ctx, "SELECT status::text FROM documents WHERE id = $1", documentID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		return status
	}

	t.Run("the list is newest first, by id within a tie, and its owner's alone", func(t *testing.T) {
		rows, err := store.ArtifactsIn(ctx, alice, nil)
		if err != nil || len(rows) != 3 {
			t.Fatalf("%d rows, %v", len(rows), err)
		}
		if rows[0].ID != 500 || rows[1].ID != 502 || rows[2].ID != 501 {
			t.Errorf("listed in the order %d, %d, %d", rows[0].ID, rows[1].ID, rows[2].ID)
		}
		thread := int64(1)
		ofThread, err := store.ArtifactsIn(ctx, alice, &thread)
		if err != nil || len(ofThread) != 1 || ofThread[0].ID != 500 {
			t.Errorf("the thread filter returned %+v, %v", ofThread, err)
		}
		// Bob's thread, asked about by Alice: the filter is still inside
		// her own artifacts, so it matches nothing of his.
		his := int64(2)
		if rows, err := store.ArtifactsIn(ctx, alice, &his); err != nil || len(rows) != 0 {
			t.Errorf("another account's thread listed %+v, %v", rows, err)
		}
		if found, err := store.VideoIn(ctx, alice, 505); err != nil || found != nil {
			t.Errorf("another account's artifact was found: %v, %v", found, err)
		}
		if found, err := store.VideoIn(ctx, bob, 505); err != nil || found == nil || found.Title != "His film" {
			t.Errorf("its owner did not find it: %v, %v", found, err)
		}
	})

	t.Run("the marker is written over the status that was read, and only that", func(t *testing.T) {
		document, err := store.DocumentOf(ctx, alice, 500)
		if err != nil || document == nil {
			t.Fatalf("%v, %v", document, err)
		}
		// Somebody who read an older status is late.
		stale := Document{ID: document.ID, Status: []byte(`{"state": "processing"}`)}
		if marked, err := store.MarkDeleting(ctx, alice, stale); err != nil || marked {
			t.Fatalf("a stale read marked the document: %v, %v", marked, err)
		}
		// Another account, even with the right status in hand, marks nothing.
		if marked, err := store.MarkDeleting(ctx, bob, *document); err != nil || marked {
			t.Fatalf("another account marked the document: %v, %v", marked, err)
		}
		if marked, err := store.MarkDeleting(ctx, alice, *document); err != nil || !marked {
			t.Fatalf("the document was not marked: %v, %v", marked, err)
		}
		if left := statusOf(document.ID); left != `{"state": "deleting"}` {
			t.Fatalf("the document is left %s", left)
		}
		// And a second delete, reading the same old status, is late too.
		if marked, err := store.MarkDeleting(ctx, alice, *document); err != nil || marked {
			t.Errorf("the document was marked twice: %v, %v", marked, err)
		}
	})

	// The specialist that makes artifacts saves them from another module
	// (soloclef_assistant/deliverables), which cannot import this package to
	// check itself against it. So the check is here: what it saves, the
	// manifest and the download read, for the owner and nobody else.
	t.Run("what the specialist saves is what the manifest and the download read", func(t *testing.T) {
		local, err := filestore.OpenLocal(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		saver := deliverables.Saver{
			Rows: deliverables.Store{Pool: pool}, Files: local, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			NewID: func() string { return "00000000000000000000000000000abc" },
			Index: func(context.Context, int64, string) error { return nil },
		}
		owner := deliverables.Owner{UserID: bob, ThreadID: 2}

		cards := make([]deliverables.Card, 5)
		for i := range cards {
			cards[i] = deliverables.Card{Front: "Q" + string(rune('1'+i)), Back: "A"}
		}
		deck, err := deliverables.NewDeck("Tides", cards)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := saver.SaveFlashcards(ctx, owner, "d1/s1", deck)
		if err != nil {
			t.Fatalf("saving the deck: %v", err)
		}
		manifest, err := store.ManifestOf(ctx, bob, saved.ArtifactID)
		if err != nil || manifest == nil {
			t.Fatalf("the manifest: %+v, %v", manifest, err)
		}
		if manifest.Format != "flashcards" || manifest.Title != "Tides" || manifest.Generation != 1 || len(manifest.Files) != 1 {
			t.Fatalf("the manifest is %+v", manifest)
		}
		file := manifest.Files[0]
		if file.ID != saved.Files[0].FileID || file.Role != "primary" || file.Backend != "local" || file.MimeType != "application/json" || len(file.Checksum) != 64 {
			t.Errorf("the deck's file is %+v", file)
		}
		// The key the row names is where the bytes are.
		body, err := local.Open(ctx, file.Key)
		if err != nil {
			t.Fatalf("the file row's key is not in the store: %v", err)
		}
		held, _ := io.ReadAll(body)
		_ = body.Close()
		if int64(len(held)) != file.Size {
			t.Errorf("the store holds %d bytes and the row says %d", len(held), file.Size)
		}
		download, err := store.DownloadOf(ctx, bob, saved.ArtifactID)
		if err != nil || download == nil || download.File == nil || download.File.Key != file.Key {
			t.Errorf("the download is %+v, %v", download, err)
		}

		// A mind map has no file: its document is what both read.
		mindMap, err := deliverables.NewMindMap("Moon", "# Moon\n* Pull\n* Phases\n")
		if err != nil {
			t.Fatal(err)
		}
		drawn, err := saver.SaveMindMap(ctx, owner, "d2/s1", mindMap)
		if err != nil {
			t.Fatalf("saving the mind map: %v", err)
		}
		const kept = "# Moon\n\n- Pull\n- Phases\n"
		if manifest, err := store.ManifestOf(ctx, bob, drawn.ArtifactID); err != nil || manifest == nil || manifest.Format != "mindmap" || manifest.Markdown != kept || len(manifest.Files) != 0 {
			t.Errorf("the mind map's manifest is %+v, %v", manifest, err)
		}
		if download, err := store.DownloadOf(ctx, bob, drawn.ArtifactID); err != nil || download == nil || download.File != nil || download.Markdown != kept || download.Title != "Moon" {
			t.Errorf("the mind map's download is %+v, %v", download, err)
		}

		// Alice is told there is no such artifact, for either.
		for _, id := range []int64{saved.ArtifactID, drawn.ArtifactID} {
			if theirs, err := store.ManifestOf(ctx, alice, id); theirs != nil || err != nil {
				t.Errorf("another account read artifact %d: %+v, %v", id, theirs, err)
			}
			if theirs, err := store.DownloadOf(ctx, alice, id); theirs != nil || err != nil {
				t.Errorf("another account downloaded artifact %d: %+v, %v", id, theirs, err)
			}
		}
	})
}
