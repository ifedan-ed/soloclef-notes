package artifacts

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// TestTheFileReadingStatementsOverARealDatabase runs the statements of the
// ten file-reading routes against Postgres itself. The route tests use a
// stand-in for the database, so this is the only place that proves the SQL
// does what the stand-in pretends: that a path into `metadata` picks out one
// user's state and nothing else, that the primary file comes first, that the
// download reads the document's Markdown only when there is no file, and --
// most of all -- that a study write changes one section of `metadata` in
// place, keeps every other key, and leaves `updated_at` alone.
//
// Like TestTheStatementsOverARealDatabase it is opt-in, and it is also the
// isolation test of these ten routes: every statement is asked about Alice's
// artifacts as Bob, and must find and change nothing.
func TestTheFileReadingStatementsOverARealDatabase(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()

	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// Alice has a deck (7700) with two files and study state, a quiz (7701)
	// with no state yet and metadata that is NULL, a video (7702), and an
	// artifact whose metadata is a list (7703). Bob has 7704, which nobody
	// asking as Alice may reach.
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, title, document_type, content, source_markdown, content_hash,
                       user_id, status, created_at)
VALUES
 (7700,'A deck','ARTIFACT','deck content','# A deck','hash-7700','`+alice+`','{"state": "ready"}','2026-06-01 00:00:00+00'),
 (7701,'A quiz','ARTIFACT','quiz content','','hash-7701','`+alice+`','{"state": "ready"}','2026-06-01 00:00:00+00'),
 (7702,'A film','ARTIFACT','film content',NULL,'hash-7702','`+alice+`','{"state": "ready"}','2026-06-01 00:00:00+00'),
 (7703,'A list','ARTIFACT','list content',NULL,'hash-7703','`+alice+`','{"state": "ready"}','2026-06-01 00:00:00+00'),
 (7704,'Elsewhere','ARTIFACT','elsewhere content',NULL,'hash-7704','`+bob+`','{"state": "ready"}','2026-06-01 00:00:00+00');
INSERT INTO artifacts (id, document_id, user_id, format, generation, metadata, updated_at, created_at)
VALUES
 (7700,7700,'`+alice+`','flashcards',3,
  '{"legacy": {"kind": "image", "id": 9, "extra": 1}, "keep": {"me": [1, 2]},
    "flashcards": {"study_by_user": {
      "`+alice+`": {"generation": 3, "marks": {"0": "good"}, "order": [1, 0]},
      "another-key": {"generation": 3, "marks": {}, "order": [0, 1]}}}}',
  '2026-06-02 00:00:00+00','2026-06-01 00:00:00+00'),
 (7701,7701,'`+alice+`','quiz',2,NULL,'2026-06-02 00:00:00+00','2026-06-01 00:00:00+00'),
 (7702,7702,'`+alice+`','video',1,
  '{"slides": [{"slide_number": 1, "audio_storage_key": "video_presentations/1/7702/slide-1-x.mp3"}],
    "scene_codes": ["a scene this route never reads"]}',
  '2026-06-02 00:00:00+00','2026-06-01 00:00:00+00'),
 (7703,7703,'`+alice+`','flashcards',1,'["not", "an", "object"]','2026-06-02 00:00:00+00','2026-06-01 00:00:00+00'),
 (7704,7704,'`+bob+`','flashcards',1,NULL,'2026-06-02 00:00:00+00','2026-06-01 00:00:00+00');
INSERT INTO artifact_files (id, artifact_id, role, storage_backend, storage_key, original_filename,
                            mime_type, size_bytes, checksum_sha256, created_at)
VALUES
 (7790,7700,'preview','local','artifacts/1/7700/preview/a.pdf','deck.pdf','application/pdf',10,'c-7790','2026-06-01 00:00:00+00'),
 (7791,7700,'primary','local','artifacts/1/7700/primary/b.json','deck.json','application/json',20,'c-7791','2026-06-01 00:00:00+00'),
 (7792,7701,'primary','local','artifacts/1/7701/primary/c.json','quiz.json','application/json',30,'c-7792','2026-06-01 00:00:00+00'),
 (7793,7704,'primary','local','artifacts/2/7704/primary/d.json','other.json','application/json',40,'c-7793','2026-06-01 00:00:00+00');
`)
	store := Store{Pool: pool}

	// metadataOf is the column as Postgres holds it, decoded, so a test can
	// compare structure rather than spacing.
	metadataOf := func(t *testing.T, artifactID int64) any {
		t.Helper()
		var raw []byte
		if err := pool.QueryRow(ctx, "SELECT metadata FROM artifacts WHERE id = $1", artifactID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if raw == nil {
			return nil
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	updatedAtOf := func(t *testing.T, artifactID int64) time.Time {
		t.Helper()
		var at time.Time
		if err := pool.QueryRow(ctx, "SELECT updated_at FROM artifacts WHERE id = $1", artifactID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	decoded := func(t *testing.T, raw string) any {
		t.Helper()
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	t.Run("the manifest reads the caller's own state, the legacy key and the files in order", func(t *testing.T) {
		row, err := store.ManifestOf(ctx, alice, 7700)
		if err != nil || row == nil {
			t.Fatalf("%v, %v", row, err)
		}
		if row.Markdown != "# A deck" || row.Generation != 3 || row.Format != "flashcards" {
			t.Errorf("the artifact reads %+v", row)
		}
		if got := decoded(t, string(row.FlashcardState)); !reflect.DeepEqual(got,
			decoded(t, `{"generation": 3, "marks": {"0": "good"}, "order": [1, 0]}`)) {
			t.Errorf("alice's state reads %s", row.FlashcardState)
		}
		if row.QuizState != nil {
			t.Errorf("a deck has a quiz state: %s", row.QuizState)
		}
		if got := decoded(t, string(row.Legacy)); !reflect.DeepEqual(got,
			decoded(t, `{"kind": "image", "id": 9, "extra": 1}`)) {
			t.Errorf("the legacy reference reads %s", row.Legacy)
		}
		if len(row.Files) != 2 || row.Files[0].ID != 7791 || row.Files[1].ID != 7790 ||
			row.Files[0].Role != "primary" || row.Files[0].Key != "artifacts/1/7700/primary/b.json" {
			t.Errorf("the files read %+v", row.Files)
		}
		// The owner of an artifact they have never studied has no state.
		unstudied, err := store.ManifestOf(ctx, bob, 7704)
		if err != nil || unstudied == nil || unstudied.FlashcardState != nil {
			t.Errorf("an unstudied deck's state: %+v, %v", unstudied, err)
		}
	})

	t.Run("an empty source_markdown reads as the document's content", func(t *testing.T) {
		row, err := store.ManifestOf(ctx, alice, 7701)
		if err != nil || row == nil || row.Markdown != "quiz content" {
			t.Fatalf("%+v, %v", row, err)
		}
	})

	t.Run("metadata that is not an object is no state, not an error", func(t *testing.T) {
		row, err := store.ManifestOf(ctx, alice, 7703)
		if err != nil || row == nil || row.FlashcardState != nil || row.Legacy != nil {
			t.Fatalf("%+v, %v", row, err)
		}
	})

	// The isolation test. Bob asks every read about each of Alice's
	// artifacts, and Alice about Bob's; nobody finds anything.
	t.Run("another account's artifact is not found by any read", func(t *testing.T) {
		for _, ask := range []struct {
			who      string
			artifact int64
			file     int64
		}{{bob, 7700, 7791}, {bob, 7701, 7792}, {bob, 7702, 0}, {alice, 7704, 7793}} {
			if manifest, err := store.ManifestOf(ctx, ask.who, ask.artifact); err != nil || manifest != nil {
				t.Errorf("manifest of %d: %+v, %v", ask.artifact, manifest, err)
			}
			if file, err := store.FileIn(ctx, ask.who, ask.artifact, ask.file); err != nil || file != nil {
				t.Errorf("file %d of %d: %+v, %v", ask.file, ask.artifact, file, err)
			}
			if download, err := store.DownloadOf(ctx, ask.who, ask.artifact); err != nil || download != nil {
				t.Errorf("download of %d: %+v, %v", ask.artifact, download, err)
			}
			if slides, err := store.SlidesOf(ctx, ask.who, ask.artifact); err != nil || slides != nil {
				t.Errorf("slides of %d: %+v, %v", ask.artifact, slides, err)
			}
			if source, err := store.StudySourceOf(ctx, ask.who, ask.artifact); err != nil || source != nil {
				t.Errorf("study source of %d: %+v, %v", ask.artifact, source, err)
			}
			if video, err := store.VideoIn(ctx, ask.who, ask.artifact); err != nil || video != nil {
				t.Errorf("video of %d: %+v, %v", ask.artifact, video, err)
			}
			if document, err := store.DocumentOf(ctx, ask.who, ask.artifact); err != nil || document != nil {
				t.Errorf("document of %d: %+v, %v", ask.artifact, document, err)
			}
			// The file list has no owner column of its own; it is reached
			// through the artifact, and must hold through it.
			files, err := New(pool).FilesOfArtifact(ctx, FilesOfArtifactParams{ArtifactID: ask.artifact, UserID: ask.who})
			if err != nil || len(files) != 0 {
				t.Errorf("files of %d: %+v, %v", ask.artifact, files, err)
			}
		}
		// Nor a file of one of Alice's artifacts named through another.
		if file, err := store.FileIn(ctx, alice, 7701, 7791); err != nil || file != nil {
			t.Errorf("a file through the wrong artifact: %+v, %v", file, err)
		}
		// And each list is its owner's alone.
		for who, want := range map[string]int{alice: 4, bob: 1} {
			if rows, err := store.ArtifactsIn(ctx, who, nil); err != nil || len(rows) != want {
				t.Errorf("%s lists %d artifacts, want %d: %v", who, len(rows), want, err)
			}
		}
	})

	// The writes. Bob can neither lock nor change Alice's deck, nor claim
	// its document for deletion, even holding the status it has.
	t.Run("another account's artifact is not changed by any write", func(t *testing.T) {
		before := metadataOf(t, 7700)
		called := false
		err := store.ChangeStudy(ctx, bob, 7700, flashcardSection, flashcardHolder,
			func(locked *LockedStudy) StudyWrite {
				called = true
				if locked != nil {
					t.Errorf("another account's artifact was locked: %+v", locked)
				}
				return StudyWrite{States: []byte(`{"written": "by bob"}`)}
			})
		if err != nil || !called {
			t.Fatalf("called %v, %v", called, err)
		}
		queries := New(pool)
		if err := queries.StoreStudyStates(ctx, StoreStudyStatesParams{
			Section: flashcardSection, Holder: flashcardHolder, States: []byte(`{"written": "by bob"}`), ID: 7700, UserID: bob,
		}); err != nil {
			t.Fatal(err)
		}
		if err := queries.ClearStudyStates(ctx, ClearStudyStatesParams{Section: flashcardSection, ID: 7700, UserID: bob}); err != nil {
			t.Fatal(err)
		}
		if after := metadataOf(t, 7700); !reflect.DeepEqual(after, before) {
			t.Errorf("another account changed the deck's metadata to %v", after)
		}
		marked, err := store.MarkDeleting(ctx, bob, Document{ID: 7700, Status: []byte(`{"state": "ready"}`)})
		if err != nil || marked {
			t.Errorf("another account marked the document for deletion: %v, %v", marked, err)
		}
		var status string
		if err := pool.QueryRow(ctx, "SELECT status::text FROM documents WHERE id = 7700").Scan(&status); err != nil || status != `{"state": "ready"}` {
			t.Errorf("the document is left %s, %v", status, err)
		}
	})

	t.Run("one file is read through its artifact and owner", func(t *testing.T) {
		file, err := store.FileIn(ctx, alice, 7700, 7790)
		if err != nil || file == nil || file.Role != "preview" || file.MimeType != "application/pdf" ||
			file.Size != 10 || file.Checksum != "c-7790" || file.Backend != "local" {
			t.Fatalf("%+v, %v", file, err)
		}
	})

	t.Run("the download reads the Markdown only when there is no file", func(t *testing.T) {
		withFile, err := store.DownloadOf(ctx, alice, 7700)
		if err != nil || withFile == nil || withFile.File == nil || withFile.File.ID != 7791 ||
			withFile.Markdown != "" || withFile.Title != "A deck" {
			t.Fatalf("with a file: %+v, %v", withFile, err)
		}
		withoutFile, err := store.DownloadOf(ctx, alice, 7702)
		if err != nil || withoutFile == nil || withoutFile.File != nil || withoutFile.Markdown != "film content" {
			t.Fatalf("without a file: %+v, %v", withoutFile, err)
		}
	})

	t.Run("a video's slides are read without its scene codes", func(t *testing.T) {
		slides, err := store.SlidesOf(ctx, alice, 7702)
		if err != nil || slides == nil || slides.Format != "video" {
			t.Fatalf("%+v, %v", slides, err)
		}
		if got := decoded(t, string(slides.Slides)); !reflect.DeepEqual(got, decoded(t,
			`[{"slide_number": 1, "audio_storage_key": "video_presentations/1/7702/slide-1-x.mp3"}]`)) {
			t.Errorf("the slides read %s", slides.Slides)
		}
	})

	t.Run("a study write's source names the primary file", func(t *testing.T) {
		source, err := store.StudySourceOf(ctx, alice, 7701)
		if err != nil || source == nil || source.Format != "quiz" || source.Generation != 2 ||
			source.File == nil || source.File.Key != "artifacts/1/7701/primary/c.json" {
			t.Fatalf("%+v, %v", source, err)
		}
		noFile, err := store.StudySourceOf(ctx, alice, 7703)
		if err != nil || noFile == nil || noFile.File != nil {
			t.Fatalf("an artifact with no file: %+v, %v", noFile, err)
		}
	})

	t.Run("a study write changes one section and nothing else", func(t *testing.T) {
		before := updatedAtOf(t, 7700)
		var seen *LockedStudy
		replacement := `{"` + alice + `": {"generation": 3, "marks": {}, "order": [0, 1]}}`
		err := store.ChangeStudy(ctx, alice, 7700, flashcardSection, flashcardHolder,
			func(locked *LockedStudy) StudyWrite {
				seen = locked
				return StudyWrite{States: []byte(replacement)}
			})
		if err != nil || seen == nil || seen.Format != "flashcards" || seen.Generation != 3 {
			t.Fatalf("%+v, %v", seen, err)
		}
		// What the decision was shown is the whole section, both keys.
		if got, ok := decoded(t, string(seen.States)).(map[string]any); !ok || len(got) != 2 {
			t.Errorf("the decision was shown %s", seen.States)
		}
		want := decoded(t, `{"legacy": {"kind": "image", "id": 9, "extra": 1}, "keep": {"me": [1, 2]},
			"flashcards": {"study_by_user": `+replacement+`}}`)
		if got := metadataOf(t, 7700); !reflect.DeepEqual(got, want) {
			t.Errorf("the metadata is now %v", got)
		}
		if after := updatedAtOf(t, 7700); !after.Equal(before) {
			t.Errorf("updated_at moved from %v to %v", before, after)
		}
	})

	t.Run("a write that is abandoned changes nothing", func(t *testing.T) {
		before := metadataOf(t, 7700)
		err := store.ChangeStudy(ctx, alice, 7700, flashcardSection, flashcardHolder,
			func(*LockedStudy) StudyWrite { return StudyWrite{Abandon: true} })
		if err != nil {
			t.Fatal(err)
		}
		if after := metadataOf(t, 7700); !reflect.DeepEqual(after, before) {
			t.Errorf("the metadata changed to %v", after)
		}
	})

	t.Run("a section nobody holds state in is removed", func(t *testing.T) {
		err := store.ChangeStudy(ctx, alice, 7700, flashcardSection, flashcardHolder,
			func(*LockedStudy) StudyWrite { return StudyWrite{} })
		if err != nil {
			t.Fatal(err)
		}
		want := decoded(t, `{"legacy": {"kind": "image", "id": 9, "extra": 1}, "keep": {"me": [1, 2]}}`)
		if got := metadataOf(t, 7700); !reflect.DeepEqual(got, want) {
			t.Errorf("the metadata is now %v", got)
		}
	})

	t.Run("NULL metadata, and metadata that is not an object, become an object", func(t *testing.T) {
		states := `{"` + alice + `": {"generation": 2}}`
		for _, artifactID := range []int64{7701, 7703} {
			section, holder := quizSection, quizHolder
			if artifactID == 7703 {
				section, holder = flashcardSection, flashcardHolder
			}
			err := store.ChangeStudy(ctx, alice, artifactID, section, holder,
				func(*LockedStudy) StudyWrite { return StudyWrite{States: []byte(states)} })
			if err != nil {
				t.Fatal(err)
			}
			want := decoded(t, `{"`+section+`": {"`+holder+`": `+states+`}}`)
			if got := metadataOf(t, artifactID); !reflect.DeepEqual(got, want) {
				t.Errorf("artifact %d's metadata is now %v", artifactID, got)
			}
		}
	})

	t.Run("an artifact that is not there is shown to the decision as nil", func(t *testing.T) {
		called := false
		err := store.ChangeStudy(ctx, alice, 999999, flashcardSection, flashcardHolder,
			func(locked *LockedStudy) StudyWrite {
				called = true
				if locked != nil {
					t.Errorf("an artifact nobody has was locked: %+v", locked)
				}
				return StudyWrite{States: []byte(`{}`)}
			})
		if err != nil || !called {
			t.Fatalf("called %v, %v", called, err)
		}
	})
}
