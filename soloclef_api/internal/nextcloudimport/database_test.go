package nextcloudimport

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres this test may empty.
// internal/testpostgres gives it the migrations' schema and two accounts,
// DELETING every account already in it, so it must never name a database
// anyone uses. With it unset the test is skipped.
//
//	docker run -d --name nextcloud-import-test -p 127.0.0.1::5432 -e POSTGRES_PASSWORD=check \
//	    --tmpfs /var/lib/postgresql/data pgvector/pgvector:pg17
//	SOLOCLEF_API_TEST_NEXTCLOUD_DATABASE=postgres://postgres:check@127.0.0.1:<port>/postgres \
//	    go test ./internal/nextcloudimport/ -run RealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_NEXTCLOUD_DATABASE"

func size(bytes int64) *int64 { return &bytes }

// seeded is the two accounts the database test works with: the owner, whose
// import it follows, and a second account that must never see or change
// any of it.
type seeded struct {
	owner, scribe string
	// cloud and drive are the owner's Nextcloud and Google Drive
	// connections; scribeCloud is the second account's Nextcloud one.
	cloud, drive, scribeCloud int64
	// notebook is the owner's; scribeNotebook the second account's.
	notebook, scribeNotebook int64
}

func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) seeded {
	t.Helper()
	var made seeded
	scan := func(target any, query string, arguments ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, query, arguments...).Scan(target); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
	connection := func(owner, kind string) int64 {
		var id int64
		scan(&id, `INSERT INTO search_source_connectors (user_id, name, connector_type, config)
			VALUES ($1::uuid, 'the ' || $2, $2::searchsourceconnectortype, '{"server_url": "https://cloud.example"}') RETURNING id`, owner, kind)
		return id
	}
	notebook := func(owner string) int64 {
		var id int64
		scan(&id, `INSERT INTO notebooks (user_id, title, updated_at) VALUES ($1::uuid, 'Reading', now() - interval '1 day') RETURNING id`, owner)
		return id
	}
	made.owner, made.scribe = testpostgres.Alice, testpostgres.Bob
	made.cloud, made.drive = connection(made.owner, "NEXTCLOUD_CONNECTOR"), connection(made.owner, "GOOGLE_DRIVE_CONNECTOR")
	made.scribeCloud = connection(made.scribe, "NEXTCLOUD_CONNECTOR")
	made.notebook, made.scribeNotebook = notebook(made.owner), notebook(made.scribe)
	return made
}

// The statements, against a real database: what the stand-in table assumes
// in the other tests is shown here to be what the SQL does.
func TestTheImportOverARealDatabase(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	var err error
	seeds := seed(t, ctx, pool)
	seedOwner, seedScribe, seedCloud, seedDrive, seedNotebook := seeds.owner, seeds.scribe, seeds.cloud, seeds.drive, seeds.notebook
	rows := PostgresRows{Pool: pool}
	account := Account{BaseURL: "https://cloud.example", Username: "someone", LoginName: "someone", AppPassword: "x"}
	notebookID := seedNotebook
	options := Options{ConnectorID: seedCloud, UserID: seedOwner, Parser: "datalab", NotebookID: &notebookID}

	type row struct {
		state, stage, parser, title, kind, content, etag string
		folder                                           *int64
		files                                            int
	}
	document := func(id int64) row {
		t.Helper()
		var found row
		var stage, parser, etag *string
		err := pool.QueryRow(ctx, `
			SELECT status ->> 'state', status ->> 'stage', parser, title, document_type::text, content,
			       document_metadata #>> '{nextcloud,etag}', folder_id,
			       (SELECT count(*) FROM document_files WHERE document_id = d.id)
			FROM documents AS d WHERE id = $1`, id).
			Scan(&found.state, &stage, &parser, &found.title, &found.kind, &found.content, &etag, &found.folder, &found.files)
		if err != nil {
			t.Fatal(err)
		}
		for target, value := range map[*string]*string{&found.stage: stage, &found.parser: parser, &found.etag: etag} {
			if value != nil {
				*target = *value
			}
		}
		return found
	}
	set := func(id int64, status string, minutesAgo int) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE documents SET status = $2::jsonb, updated_at = now() - make_interval(mins => $3) WHERE id = $1`,
			id, status, minutesAgo); err != nil {
			t.Fatal(err)
		}
	}
	count := func(query string, arguments ...any) int {
		t.Helper()
		var counted int
		if err := pool.QueryRow(ctx, query, arguments...).Scan(&counted); err != nil {
			t.Fatal(err)
		}
		return counted
	}

	t.Run("a connection is read for its owner, and only a Nextcloud one", func(t *testing.T) {
		if config, found, err := rows.Connection(ctx, seedCloud, seedOwner); err != nil || !found || !json.Valid(config) {
			t.Errorf("the owner's connection: %v, %v", found, err)
		}
		if _, found, _ := rows.Connection(ctx, seedCloud, seedScribe); found {
			t.Error("somebody else read the connection")
		}
		if _, found, _ := rows.Connection(ctx, seedDrive, seedOwner); found {
			t.Error("a Google Drive connection was read as a Nextcloud one")
		}
		if _, found, _ := rows.Connection(ctx, seeds.scribeCloud, seedOwner); found {
			t.Error("the owner read somebody else's connection")
		}
		if owned, _ := rows.OwnsNotebook(ctx, seedNotebook, seedOwner); !owned {
			t.Error("the notebook is not found for its owner")
		}
		if owned, _ := rows.OwnsNotebook(ctx, seedNotebook, seedScribe); owned {
			t.Error("the notebook is found for another account")
		}
	})

	plan := Plan{
		PickedFolders: []PickedFolder{{Path: "/Work/Plans", Name: "Plans"}},
		Files: []PlannedFile{
			{Entry: Entry{Path: "/Work/Plans/a.pdf", Name: "a.pdf", Size: size(4), ETag: "ea", ContentType: "application/pdf"}, Folders: []string{"Plans"}, PickedFolder: "/Work/Plans"},
			{Entry: Entry{Path: "/Work/Plans/Old/b.pdf", Name: "b.pdf", Size: size(5), ETag: "eb"}, Folders: []string{"Plans", "Old"}, PickedFolder: "/Work/Plans"},
			{Entry: Entry{Path: "/loose.pdf", Name: "loose.pdf", ETag: "el"}},
		},
	}
	var first Started
	t.Run("an import writes its documents, folders and links in one transaction", func(t *testing.T) {
		first, err = rows.Start(ctx, plan, account, options)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.DocumentIDs) != 3 || len(first.Jobs) != 3 || first.Reused != 0 || len(first.FolderIDs) != 1 {
			t.Fatalf("started %+v", first)
		}
		a := document(first.DocumentIDs[0])
		// Waiting for its download, naming its parser, with no placeholder
		// text where a search would find it.
		want := row{state: "pending", stage: "downloading", parser: "datalab", title: "a.pdf", kind: "FILE", content: "", etag: "ea", folder: &first.FolderIDs[0]}
		if !reflect.DeepEqual(a, want) {
			t.Errorf("a.pdf is %+v, want %+v", a, want)
		}
		// b.pdf is in Old, which is in Plans.
		b := document(first.DocumentIDs[1])
		var parent *int64
		var name string
		if err := pool.QueryRow(ctx, `SELECT name, parent_id FROM folders WHERE id = $1`, b.folder).Scan(&name, &parent); err != nil ||
			name != "Old" || parent == nil || *parent != first.FolderIDs[0] {
			t.Errorf("b.pdf is in %q under %v, %v", name, parent, err)
		}
		if loose := document(first.DocumentIDs[2]); loose.folder != nil {
			t.Errorf("a file picked on its own was put in folder %d", *loose.folder)
		}
		// The facts a refresh reads back.
		imported, err := rows.Imported(ctx, first.DocumentIDs[0], seedOwner)
		if err != nil || imported.ConnectorID != seedCloud || imported.ServerURL != account.BaseURL || imported.Username != "someone" ||
			imported.Path != "/Work/Plans/a.pdf" || imported.ETag != "ea" || imported.ContentType != "application/pdf" ||
			imported.State != "pending" || imported.Stage != "downloading" || imported.Stale {
			t.Errorf("imported %+v, %v", imported, err)
		}
		// The notebook is linked to the picked folder, and to the loose
		// file; a file inside the folder reaches it through the folder.
		if got := count(`SELECT count(*) FROM notebook_sources WHERE notebook_id = $1 AND folder_id = $2 AND user_id = $3::uuid`, seedNotebook, first.FolderIDs[0], seedOwner); got != 1 {
			t.Errorf("%d links to the folder", got)
		}
		if got := count(`SELECT count(*) FROM notebook_sources WHERE notebook_id = $1 AND document_id = ANY($2)`, seedNotebook, first.DocumentIDs); got != 1 {
			t.Errorf("%d links to documents, want the loose file's only", got)
		}
		if got := count(`SELECT count(*) FROM notebooks WHERE id = $1 AND updated_at > now() - interval '1 minute'`, seedNotebook); got != 1 {
			t.Error("the notebook was not marked active")
		}
	})

	t.Run("picked again at once, everything is in flight and nothing is started twice", func(t *testing.T) {
		again, err := rows.Start(ctx, plan, account, options)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(again.DocumentIDs, first.DocumentIDs) || !reflect.DeepEqual(again.FolderIDs, first.FolderIDs) || len(again.Jobs) != 0 || again.Reused != 3 {
			t.Errorf("the second import started %+v", again)
		}
		if got := count(`SELECT count(*) FROM folders WHERE user_id = $1::uuid AND name IN ('Plans', 'Old')`, seedOwner); got != 2 {
			t.Errorf("%d folders; the second import made its own", got)
		}
	})

	a, b, loose := int64(0), int64(0), int64(0)
	if len(first.DocumentIDs) == 3 {
		a, b, loose = first.DocumentIDs[0], first.DocumentIDs[1], first.DocumentIDs[2]
	}

	t.Run("a download that was abandoned is started again, and only that one", func(t *testing.T) {
		// a.pdf has waited sixteen minutes for a process that is gone;
		// b.pdf fourteen, which is still in flight.
		set(a, `{"state": "pending", "stage": "downloading"}`, 16)
		set(b, `{"state": "pending", "stage": "downloading"}`, 14)
		again, err := rows.Start(ctx, plan, account, options)
		if err != nil {
			t.Fatal(err)
		}
		if len(again.Jobs) != 1 || again.Jobs[0].DocumentID != a || again.Jobs[0].Path != "/Work/Plans/a.pdf" || again.Reused != 2 {
			t.Errorf("started %+v", again)
		}
		// Queued again, it is fresh: a third import leaves it alone.
		if third, _ := rows.Start(ctx, plan, account, options); len(third.Jobs) != 0 {
			t.Errorf("the document was started a third time: %+v", third.Jobs)
		}
	})

	t.Run("a downloaded file is recorded once, and hands the document to the indexer", func(t *testing.T) {
		file := Downloaded{DocumentID: a, UserID: seedOwner, Backend: "s3", Key: "documents/" + seedOwner + "/1/original/aaaa.pdf", Name: "a.pdf", ContentType: "application/pdf", Size: 4, SHA256: "abc"}
		if wanted, err := rows.RecordDownloaded(ctx, file); err != nil || !wanted {
			t.Fatalf("%v, %v", wanted, err)
		}
		after := document(a)
		if after.state != "pending" || after.stage != "" || after.files != 1 {
			t.Errorf("a.pdf is %+v", after)
		}
		var backend, key, name, checksum, contentHash string
		var bytes int64
		if err := pool.QueryRow(ctx, `
			SELECT f.storage_backend, f.storage_key, f.original_filename, f.checksum_sha256, f.size_bytes, d.content_hash
			FROM document_files AS f JOIN documents AS d ON d.id = f.document_id
			WHERE f.document_id = $1 AND f.kind = 'ORIGINAL'`, a).Scan(&backend, &key, &name, &checksum, &bytes, &contentHash); err != nil ||
			backend != "s3" || key != file.Key || name != "a.pdf" || checksum != "abc" || bytes != 4 || contentHash != "abc" {
			t.Errorf("the file row: %s %s %s %s %d, content_hash %s, %v", backend, key, name, checksum, bytes, contentHash, err)
		}
		// A second job that fetched the same file is told its copy is not
		// wanted, and writes nothing.
		if wanted, err := rows.RecordDownloaded(ctx, file); err != nil || wanted {
			t.Errorf("a second copy was recorded: %v, %v", wanted, err)
		}
		if document(a).files != 1 {
			t.Error("a second file row was written")
		}
		// Stored and waiting for the indexer is not a stuck download,
		// however long it waits.
		set(a, `{"state": "pending"}`, 600)
		if again, _ := rows.Start(ctx, plan, account, options); len(again.Jobs) != 0 {
			t.Errorf("a document waiting for the indexer was downloaded again: %+v", again.Jobs)
		}
	})

	t.Run("a ready file is reused while its etag is the same, and read again when it changes", func(t *testing.T) {
		set(a, `{"state": "ready"}`, 0)
		set(b, `{"state": "ready"}`, 0)
		changed := plan
		changed.Files = append([]PlannedFile(nil), plan.Files...)
		changed.Files[1].Entry.ETag = "eb-2"
		local := options
		local.Parser = "local"
		again, err := rows.Start(ctx, changed, account, local)
		if err != nil {
			t.Fatal(err)
		}
		if len(again.Jobs) != 1 || again.Jobs[0].DocumentID != b {
			t.Fatalf("started %+v", again.Jobs)
		}
		// The unchanged one keeps everything, parser included.
		if kept := document(a); kept.state != "ready" || kept.parser != "datalab" {
			t.Errorf("a.pdf is %+v", kept)
		}
		// The changed one waits for its file, knows the new etag, and is
		// read by the parser this import chose.
		if queued := document(b); queued.state != "pending" || queued.stage != "downloading" || queued.etag != "eb-2" || queued.parser != "local" {
			t.Errorf("b.pdf is %+v", queued)
		}
	})

	t.Run("a failure is recorded only on a document still waiting for its download", func(t *testing.T) {
		if err := rows.MarkFailed(ctx, b, seedOwner, "Nextcloud could not be reached."); err != nil {
			t.Fatal(err)
		}
		if failed := document(b); failed.state != "failed" {
			t.Errorf("b.pdf is %+v", failed)
		}
		if err := rows.MarkFailed(ctx, a, seedOwner, "too late"); err != nil {
			t.Fatal(err)
		}
		if untouched := document(a); untouched.state != "ready" {
			t.Errorf("a ready document was marked failed: %+v", untouched)
		}
		// And a failed one is simply read again by the next import.
		if again, _ := rows.Start(ctx, plan, account, options); len(again.Jobs) != 1 || again.Jobs[0].DocumentID != b {
			t.Errorf("after a failure, started %+v", again.Jobs)
		}
	})

	t.Run("a refresh is queued once", func(t *testing.T) {
		set(a, `{"state": "ready"}`, 0)
		entry := Entry{Path: "/Work/Plans/a.pdf", Name: "a.pdf", ETag: "ea-2", Size: size(9)}
		if queued, err := rows.QueueRefresh(ctx, a, seedOwner, 999, entry); err != nil || !queued {
			t.Fatalf("%v, %v", queued, err)
		}
		imported, _ := rows.Imported(ctx, a, seedOwner)
		// The new etag and the connection that refreshed it; the path and
		// the account it came from are kept.
		if imported.ETag != "ea-2" || imported.ConnectorID != 999 || imported.Path != "/Work/Plans/a.pdf" || imported.Username != "someone" ||
			imported.State != "pending" || imported.Stage != "downloading" {
			t.Errorf("imported %+v", imported)
		}
		if queued, err := rows.QueueRefresh(ctx, a, seedOwner, 999, entry); err != nil || queued {
			t.Errorf("a second refresh was queued: %v, %v", queued, err)
		}
	})

	t.Run("a document being deleted stops the whole import", func(t *testing.T) {
		set(loose, `{"state": "deleting"}`, 0)
		fresh := Plan{
			PickedFolders: []PickedFolder{{Path: "/New", Name: "New"}},
			Files: []PlannedFile{
				{Entry: Entry{Path: "/New/n.pdf", Name: "n.pdf"}, Folders: []string{"New"}, PickedFolder: "/New"},
				plan.Files[2],
			},
		}
		_, err := rows.Start(ctx, fresh, account, options)
		var deleting *errBeingDeleted
		if !errors.As(err, &deleting) || deleting.name != "loose.pdf" {
			t.Fatalf("error %v", err)
		}
		// Nothing of it was kept: not the folder, not the other document.
		if got := count(`SELECT count(*) FROM folders WHERE user_id = $1::uuid AND name = 'New'`, seedOwner); got != 0 {
			t.Error("the folder of a refused import was kept")
		}
		if got := count(`SELECT count(*) FROM documents WHERE user_id = $1::uuid AND title = 'n.pdf'`, seedOwner); got != 0 {
			t.Error("a document of a refused import was kept")
		}
	})

	// The second account, against everything the owner's import made.
	t.Run("another account sees and changes none of it", func(t *testing.T) {
		owners := func(id int64) row { return document(id) }
		before := owners(a)
		var beforeFiles int
		beforeFiles = before.files

		// Not read: the owner's document is nil for the other account,
		// exactly as an id nobody has.
		if imported, err := rows.Imported(ctx, a, seedScribe); err != nil || imported != nil {
			t.Errorf("another account read the document: %+v, %v", imported, err)
		}
		if never, err := rows.Imported(ctx, 1<<40, seedScribe); err != nil || never != nil {
			t.Errorf("an id nobody has: %+v, %v", never, err)
		}
		// Not refreshed, not failed, not given a file.
		set(a, `{"state": "ready"}`, 0)
		if queued, err := rows.QueueRefresh(ctx, a, seedScribe, 999, Entry{Path: "/Work/Plans/a.pdf", Name: "a.pdf", ETag: "theirs"}); err != nil || queued {
			t.Errorf("another account queued a refresh: %v, %v", queued, err)
		}
		set(a, `{"state": "pending", "stage": "downloading"}`, 0)
		if err := rows.MarkFailed(ctx, a, seedScribe, "not theirs to fail"); err != nil {
			t.Fatal(err)
		}
		if wanted, err := rows.RecordDownloaded(ctx, Downloaded{DocumentID: a, UserID: seedScribe, Backend: "s3", Key: "documents/x/1/original/b.pdf", Name: "a.pdf", Size: 1, SHA256: "theirs"}); err != nil || wanted {
			t.Errorf("another account recorded a file on the document: %v, %v", wanted, err)
		}
		if after := owners(a); after.state != "pending" || after.stage != "downloading" || after.etag != before.etag || after.files != beforeFiles {
			t.Errorf("the document changed under another account's requests: %+v, was %+v", after, before)
		}
		set(a, `{"state": "ready"}`, 0)

		// The same Nextcloud account, server and paths imported by the
		// second account (a drive two people share): its own documents and
		// its own folders, and the owner's are neither reused nor moved.
		theirs := Options{ConnectorID: seeds.scribeCloud, UserID: seedScribe, Parser: "local"}
		started, err := rows.Start(ctx, plan, account, theirs)
		if err != nil {
			t.Fatal(err)
		}
		if len(started.DocumentIDs) != 3 || len(started.Jobs) != 3 || started.Reused != 0 || len(started.FolderIDs) != 1 {
			t.Fatalf("the second account's import started %+v; it must be all new", started)
		}
		for _, id := range append(append([]int64{}, started.DocumentIDs...), started.FolderIDs...) {
			for _, owned := range append(append([]int64{}, first.DocumentIDs...), first.FolderIDs...) {
				if id == owned {
					t.Errorf("the second account's import was given the owner's row %d", id)
				}
			}
		}
		if got := count(`SELECT count(*) FROM documents WHERE id = ANY($1) AND user_id = $2::uuid`, started.DocumentIDs, seedScribe); got != 3 {
			t.Errorf("%d of the second account's 3 documents are its own", got)
		}
		if got := count(`SELECT count(*) FROM documents WHERE id = ANY($1) AND user_id = $2::uuid`, first.DocumentIDs, seedOwner); got != 3 {
			t.Errorf("%d of the owner's 3 documents are still the owner's", got)
		}
		for _, job := range started.Jobs {
			if job.UserID != seedScribe {
				t.Errorf("a download of the second account's import is owed to %s", job.UserID)
			}
		}

		// An import that names the owner's notebook writes nothing at all:
		// the transaction stops before the first folder.
		foreign := theirs
		foreign.NotebookID = &notebookID
		linksBefore := count(`SELECT count(*) FROM notebook_sources WHERE notebook_id = $1`, seedNotebook)
		_, err = rows.Start(ctx, Plan{PickedFolders: []PickedFolder{{Path: "/Theirs", Name: "Theirs"}}}, account, foreign)
		if !errors.Is(err, errNotebookGone) {
			t.Fatalf("an import linked to another account's notebook: %v", err)
		}
		if got := count(`SELECT count(*) FROM folders WHERE name = 'Theirs'`); got != 0 {
			t.Error("a folder of the refused import was kept")
		}
		if got := count(`SELECT count(*) FROM notebook_sources WHERE notebook_id = $1`, seedNotebook); got != linksBefore {
			t.Errorf("the owner's notebook has %d links, had %d", got, linksBefore)
		}
		// Even past the application's check, the database refuses a link
		// between one account's notebook and another's document.
		if _, err := pool.Exec(ctx, `INSERT INTO notebook_sources (notebook_id, user_id, document_id) VALUES ($1, $2::uuid, $3)`,
			seedNotebook, seedOwner, started.DocumentIDs[0]); err == nil {
			t.Error("the database linked the owner's notebook to another account's document")
		}
	})

	t.Run("a new folder sorts after its siblings", func(t *testing.T) {
		later, err := rows.Start(ctx, Plan{PickedFolders: []PickedFolder{{Path: "/Zebra", Name: "Zebra"}, {Path: "/Aardvark", Name: "Aardvark"}}}, account, options)
		if err != nil || len(later.FolderIDs) != 2 {
			t.Fatalf("%+v, %v", later, err)
		}
		// By position, not by name: each is appended after the last.
		var ordered []string
		found, err := pool.Query(ctx, `SELECT name FROM folders WHERE user_id = $1::uuid AND parent_id IS NULL AND name IN ('Plans', 'Zebra', 'Aardvark') ORDER BY position`, seedOwner)
		if err != nil {
			t.Fatal(err)
		}
		for found.Next() {
			var name string
			_ = found.Scan(&name)
			ordered = append(ordered, name)
		}
		if want := []string{"Plans", "Zebra", "Aardvark"}; !reflect.DeepEqual(ordered, want) {
			t.Errorf("folders in order %v, want %v", ordered, want)
		}
	})
}
