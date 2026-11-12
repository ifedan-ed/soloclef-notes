package uploads

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres this test may empty
// (internal/testpostgres deletes every account in it). Unset, the test is
// skipped.
//
//	SOLOCLEF_API_TEST_UPLOADS_DATABASE=postgres://postgres:check@127.0.0.1:<port>/postgres \
//	    go test ./internal/uploads/ -run RealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_UPLOADS_DATABASE"

// staged is a file as the route hands it to the transaction: stored under
// the key of the document the first read chose.
func staged(t *testing.T, ctx context.Context, rows PostgresRows, userID, name, identity, content string) Staged {
	t.Helper()
	file := Staged{Name: name, Identity: identity, Backend: "s3", Size: int64(len(content)), SHA256: digest(content)}
	prior, err := rows.UploadNamed(ctx, userID, identity)
	if err != nil {
		t.Fatal(err)
	}
	if prior != nil {
		file.DocumentID, file.Existing = prior.ID, true
	} else if file.DocumentID, err = rows.NextDocumentID(ctx); err != nil {
		t.Fatal(err)
	}
	if file.Key, err = OriginalKey(userID, file.DocumentID, name); err != nil {
		t.Fatal(err)
	}
	return file
}

// owner is who a document belongs to, and how many file rows it has.
func ownerOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, documentID int64) (owner, state string, files int) {
	t.Helper()
	if err := pool.QueryRow(ctx, `
		SELECT d.user_id::text, d.status ->> 'state',
		       (SELECT count(*) FROM document_files AS f WHERE f.document_id = d.id AND f.user_id = d.user_id)
		FROM documents AS d WHERE d.id = $1`, documentID).Scan(&owner, &state, &files); err != nil {
		t.Fatal(err)
	}
	return owner, state, files
}

// The upload's statements against Postgres, with two accounts. The route
// tests use a stand-in for the database; that each statement names the
// owner is a property of the SQL, and is shown here.
func TestUploadsOverARealDatabaseKeepTwoAccountsApart(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	rows := PostgresRows{Pool: pool}

	// Alice uploads report.pdf.
	hers := staged(t, ctx, rows, alice, "report.pdf", Identity("report.pdf", alice), "the report")
	recorded, err := rows.Record(ctx, Batch{UserID: alice, Parser: "local", Files: []Staged{hers}})
	if err != nil || len(recorded) != 1 || recorded[0].Outcome != Created {
		t.Fatalf("Alice's upload: %+v, %v", recorded, err)
	}
	aliceDocument := recorded[0].DocumentID
	if owner, state, files := ownerOf(t, ctx, pool, aliceDocument); owner != alice || state != "pending" || files != 1 {
		t.Fatalf("Alice's document is %s's, %s, with %d files", owner, state, files)
	}

	t.Run("the vision switch is stored, and a re-upload's replaces it", func(t *testing.T) {
		scan := staged(t, ctx, rows, alice, "scan.pdf", Identity("scan.pdf", alice), "a scan")
		recorded, err := rows.Record(ctx, Batch{UserID: alice, Parser: "local", Vision: true, Files: []Staged{scan}})
		if err != nil || recorded[0].Outcome != Created {
			t.Fatalf("the upload: %+v, %v", recorded, err)
		}
		vision := func() bool {
			var on bool
			if err := pool.QueryRow(ctx, `SELECT use_vision_llm FROM documents WHERE id = $1`, recorded[0].DocumentID).Scan(&on); err != nil {
				t.Fatal(err)
			}
			return on
		}
		if !vision() {
			t.Fatal("use_vision_llm was not stored")
		}
		if _, err := pool.Exec(ctx, `UPDATE documents SET status = '{"state": "ready"}' WHERE id = $1`, recorded[0].DocumentID); err != nil {
			t.Fatal(err)
		}
		again := staged(t, ctx, rows, alice, "scan.pdf", Identity("scan.pdf", alice), "a better scan")
		if replaced, err := rows.Record(ctx, Batch{UserID: alice, Parser: "local", Files: []Staged{again}}); err != nil || replaced[0].Outcome != Replaced {
			t.Fatalf("the re-upload: %+v, %v", replaced, err)
		}
		if vision() {
			t.Error("a re-upload with vision off left it on")
		}
	})

	t.Run("the same file name is each account's own document", func(t *testing.T) {
		his := staged(t, ctx, rows, bob, "report.pdf", Identity("report.pdf", bob), "the report")
		if his.Existing || his.DocumentID == aliceDocument {
			t.Fatalf("Bob's file was staged for Alice's document: %+v", his)
		}
		recorded, err := rows.Record(ctx, Batch{UserID: bob, Parser: "local", Files: []Staged{his}})
		if err != nil || recorded[0].Outcome != Created || recorded[0].DocumentID == aliceDocument {
			t.Fatalf("Bob's upload: %+v, %v", recorded, err)
		}
		if owner, _, files := ownerOf(t, ctx, pool, recorded[0].DocumentID); owner != bob || files != 1 {
			t.Errorf("Bob's document is %s's with %d files", owner, files)
		}
		if owner, _, files := ownerOf(t, ctx, pool, aliceDocument); owner != alice || files != 1 {
			t.Errorf("Alice's document is now %s's with %d files", owner, files)
		}
	})

	t.Run("an identity is found only for its owner", func(t *testing.T) {
		// Even holding the very hash Alice's document is stored under, Bob
		// is told there is no such document.
		if prior, err := rows.UploadNamed(ctx, bob, Identity("report.pdf", alice)); err != nil || prior != nil {
			t.Errorf("Bob found Alice's document by its identity: %+v, %v", prior, err)
		}
		if prior, err := rows.UploadNamed(ctx, alice, Identity("report.pdf", alice)); err != nil || prior == nil || prior.ID != aliceDocument {
			t.Errorf("Alice does not find her own: %+v, %v", prior, err)
		}
	})

	t.Run("a batch that names another account's document writes nothing", func(t *testing.T) {
		// What a route with a bug, or a forged first read, would hand the
		// transaction: Bob as the owner, Alice's document as the target.
		forged := Staged{
			DocumentID: aliceDocument, Existing: true, Name: "report.pdf", Identity: Identity("report.pdf", alice),
			Backend: "s3", Key: "documents/" + bob + "/1/original/x.pdf", Size: 3, SHA256: digest("new"),
		}
		_, err := rows.Record(ctx, Batch{UserID: bob, Parser: "datalab", Files: []Staged{forged}})
		var moved *ErrMovedMeanwhile
		if !errors.As(err, &moved) {
			t.Fatalf("the forged batch: %v, want it refused as changed meanwhile", err)
		}
		var parser, checksum string
		if err := pool.QueryRow(ctx, `SELECT parser, content_hash FROM documents WHERE id = $1`, aliceDocument).Scan(&parser, &checksum); err != nil {
			t.Fatal(err)
		}
		if _, _, files := ownerOf(t, ctx, pool, aliceDocument); files != 1 || parser != "local" || checksum != digest("the report") {
			t.Errorf("Alice's document was changed: %d files, parser %s, hash %s", files, parser, checksum)
		}
		// And the database itself refuses a file row that names one
		// account and another's document.
		if _, err := pool.Exec(ctx, `
			INSERT INTO document_files (document_id, user_id, storage_backend, storage_key, original_filename, size_bytes)
			VALUES ($1, $2::uuid, 's3', 'k', 'report.pdf', 1)`, aliceDocument, bob); err == nil {
			t.Error("the database took a file row for Alice's document owned by Bob")
		}
	})

	t.Run("a folder upload cannot go under another account's folder", func(t *testing.T) {
		folderFile := func(userID, relative, content string) FolderFile {
			return FolderFile{Staged: staged(t, ctx, rows, userID, relative, FolderIdentity("Thesis", relative, userID), content), RelativePath: relative}
		}
		hers, err := rows.RecordFolder(ctx, FolderBatch{UserID: alice, Parser: "local", FolderName: "Thesis", Files: []FolderFile{folderFile(alice, "a.md", "A")}})
		if err != nil || hers.RootFolderID == 0 {
			t.Fatalf("Alice's folder upload: %+v, %v", hers, err)
		}

		// Before any file is read, and again inside the transaction.
		if found, err := rows.FolderIn(ctx, hers.RootFolderID, bob); err != nil || found {
			t.Errorf("Alice's folder is there for Bob: %v, %v", found, err)
		}
		if found, err := rows.FolderIn(ctx, hers.RootFolderID, alice); err != nil || !found {
			t.Errorf("Alice's folder is not there for Alice: %v, %v", found, err)
		}
		root := hers.RootFolderID
		_, err = rows.RecordFolder(ctx, FolderBatch{UserID: bob, Parser: "local", FolderName: "Thesis", RootFolderID: &root,
			Files: []FolderFile{folderFile(bob, "b.md", "B")}})
		if !errors.Is(err, ErrRootFolderGone) {
			t.Fatalf("Bob's files under Alice's folder: %v, want the folder reported gone", err)
		}
		var inside int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM documents WHERE folder_id = $1`, root).Scan(&inside); err != nil || inside != 1 {
			t.Errorf("Alice's folder holds %d documents, %v; want only her own", inside, err)
		}

		// The same folder name synced by Bob is his own folder, with his
		// own document, although the names and paths are Alice's.
		his, err := rows.RecordFolder(ctx, FolderBatch{UserID: bob, Parser: "local", FolderName: "Thesis", Files: []FolderFile{folderFile(bob, "a.md", "A")}})
		if err != nil || his.RootFolderID == hers.RootFolderID || his.Files[0].Outcome != Created || his.Files[0].DocumentID == hers.Files[0].DocumentID {
			t.Fatalf("Bob's own folder upload: %+v, %v (Alice's was %+v)", his, err, hers)
		}
		var folderOwner string
		if err := pool.QueryRow(ctx, `SELECT user_id::text FROM folders WHERE id = $1`, his.RootFolderID).Scan(&folderOwner); err != nil || folderOwner != bob {
			t.Errorf("Bob's folder belongs to %s, %v", folderOwner, err)
		}
	})
}
