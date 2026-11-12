package deliverables

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// databaseVariable names a throwaway Postgres at the schema (a
// pgvector/pgvector:pg17 brought up to date by soloclef_schema's
// `migrate up`). The test **truncates the tables it seeds**, so it must
// never name a database anyone uses. With it unset the test is skipped.
//
//	SOLOCLEF_API_TEST_DELIVERABLES_DATABASE=postgres://check:check@127.0.0.1:39841/check \
//	  go test ./deliverables/ -run OverARealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_DELIVERABLES_DATABASE"

// The accounts of these tests. Ben is the one who asks; Ada is somebody
// else, who must never be able to see or reach what Ben's turn made.
const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
	cat = "cccccccc-0000-4000-8000-000000000003"
)

// seed is three accounts, with a thread of Ben's (1) and one of Ada's (9).
const seed = `
TRUNCATE "user" CASCADE;
INSERT INTO "user" (id, email) VALUES
  ('` + ada + `', 'ada@example.org'),
  ('` + ben + `', 'ben@example.org'),
  ('` + cat + `', 'cat@example.org');
INSERT INTO new_chat_threads (id, title, user_id) VALUES
  (1, 'New chat', '` + ben + `'),
  (9, 'Ada''s chat', '` + ada + `');
`

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTheKindsOverARealDatabase saves each kind with the generated
// statements against the real constraints, and reads each back as its
// owner and as somebody else (readback_test.go).
func TestTheKindsOverARealDatabase(t *testing.T) {
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	local, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var indexed []int64
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	saver := Saver{
		Rows: Store{Pool: pool}, Files: local, Logger: quiet,
		NewID: func() string { return "00000000000000000000000000000abc" },
		Index: func(_ context.Context, documentID int64, _ string) error {
			indexed = append(indexed, documentID)
			return nil
		},
	}
	owner := Owner{ThreadID: 1, UserID: ben}
	read := savedRows{Pool: pool}

	t.Run("a quiz is rows and a file the artifact routes read", func(t *testing.T) {
		quiz, err := NewQuiz("Cells", questions(6))
		if err != nil {
			t.Fatal(err)
		}
		saved, err := saver.SaveQuiz(ctx, owner, "d1/s1", quiz)
		if err != nil {
			t.Fatalf("saving: %v", err)
		}
		manifest, err := read.ManifestOf(ctx, ben, saved.ArtifactID)
		if err != nil || manifest == nil {
			t.Fatalf("manifest: %+v, %v", manifest, err)
		}
		if manifest.Format != "quiz" || manifest.Title != "Cells" || manifest.Generation != 1 || manifest.Markdown != quiz.Markdown() || len(manifest.Files) != 1 {
			t.Fatalf("manifest = %+v", manifest)
		}
		file := manifest.Files[0]
		if file.ID != saved.Files[0].FileID || file.Role != "primary" || file.Filename != "cells.json" || file.MimeType != "application/json" ||
			file.Backend != "local" || file.Key != "artifacts/"+ben+"/"+strconv.FormatInt(saved.ArtifactID, 10)+"/primary/00000000000000000000000000000abc.json" || len(file.Checksum) != 64 {
			t.Errorf("file = %+v", file)
		}
		body, err := local.Open(ctx, file.Key)
		if err != nil {
			t.Fatalf("the file row's key is not in the store: %v", err)
		}
		stored, _ := io.ReadAll(body)
		body.Close()
		want, _ := quiz.File()
		if string(stored) != string(want) || file.Size != int64(len(want)) {
			t.Errorf("stored %d bytes, the row says %d, the quiz is %d", len(stored), file.Size, len(want))
		}
		var creator, docOwner, docType, state, metadata string
		if err := pool.QueryRow(ctx, `SELECT a.user_id::text, d.user_id::text, d.document_type::text, d.status ->> 'state', a.metadata::text
			FROM artifacts a JOIN documents d ON d.id = a.document_id WHERE a.id = $1`, saved.ArtifactID).Scan(&creator, &docOwner, &docType, &state, &metadata); err != nil {
			t.Fatal(err)
		}
		if creator != ben || docOwner != ben || docType != "ARTIFACT" || state != "pending" || !strings.Contains(metadata, `"verified": true`) {
			t.Errorf("rows: artifact of %s, document of %s, %s, %s, %s", creator, docOwner, docType, state, metadata)
		}
		// Another account asking for the same artifact is told there is
		// none: the artifact is Ben's and nobody else's.
		if theirs, err := read.ManifestOf(ctx, ada, saved.ArtifactID); theirs != nil || err != nil {
			t.Errorf("Ada read Ben's artifact: %+v, %v", theirs, err)
		}
	})

	t.Run("a mind map is its document and no file", func(t *testing.T) {
		mindMap, err := NewMindMap("Photosynthesis", "# Photosynthesis\n* Light reactions\n    * Photosystem II\n* Calvin cycle\n")
		if err != nil {
			t.Fatal(err)
		}
		filesBefore := count(t, pool, "artifact_files")
		saved, err := saver.SaveMindMap(ctx, owner, "d2/s1", mindMap)
		if err != nil {
			t.Fatalf("saving: %v", err)
		}
		if len(saved.Files) != 0 || saved.Files == nil || saved.Format != "mindmap" {
			t.Errorf("saved = %+v", saved)
		}
		if count(t, pool, "artifact_files") != filesBefore {
			t.Error("a file row was written for a mind map")
		}
		// What the viewer draws from, and what the download button sends.
		const stored = "# Photosynthesis\n\n- Light reactions\n  - Photosystem II\n- Calvin cycle\n"
		manifest, err := read.ManifestOf(ctx, ben, saved.ArtifactID)
		if err != nil || manifest == nil || manifest.Format != "mindmap" || manifest.Markdown != stored || len(manifest.Files) != 0 {
			t.Fatalf("manifest = %+v, %v", manifest, err)
		}
		download, err := read.DownloadOf(ctx, ben, saved.ArtifactID)
		if err != nil || download == nil || download.File != nil || download.Markdown != stored || download.Title != "Photosynthesis" {
			t.Errorf("download = %+v, %v", download, err)
		}
		var path, content, source string
		if err := pool.QueryRow(ctx, `SELECT d.path, d.content, d.source_markdown FROM artifacts a JOIN documents d ON d.id = a.document_id WHERE a.id = $1`, saved.ArtifactID).Scan(&path, &content, &source); err != nil {
			t.Fatal(err)
		}
		if path != "/documents/Photosynthesis.md" || content != stored || source != stored {
			t.Errorf("document %s: %q / %q", path, content, source)
		}
		// The same title is another account's own document, at the same
		// path: names collide within an account, never across two.
		theirs, err := saver.SaveMindMap(ctx, Owner{ThreadID: 9, UserID: ada}, "d2/s2", mindMap)
		if err != nil {
			t.Fatalf("Ada's mind map of the same title: %v", err)
		}
		var theirPath, theirOwner string
		if err := pool.QueryRow(ctx, `SELECT d.path, d.user_id::text FROM artifacts a JOIN documents d ON d.id = a.document_id WHERE a.id = $1`, theirs.ArtifactID).Scan(&theirPath, &theirOwner); err != nil || theirPath != path || theirOwner != ada {
			t.Errorf("Ada's document is %s of %s, %v", theirPath, theirOwner, err)
		}
		// A save for an account that is not there leaves nothing behind.
		before := count(t, pool, "documents")
		if _, err := saver.SaveMindMap(ctx, Owner{ThreadID: 1, UserID: "dddddddd-0000-4000-8000-000000000004"}, "x", mindMap); err == nil || count(t, pool, "documents") != before {
			t.Errorf("a mind map for an account that does not exist: %v", err)
		}
	})

	if len(indexed) != 3 {
		t.Errorf("indexing was asked for %v", indexed)
	}

	t.Run("a podcast is a row at the brief gate", func(t *testing.T) {
		rows := PodcastStore{Pool: pool}
		open := Podcasts{Rows: rows, Voices: kokoro(t, "af_heart", "am_adam"), Brief: PodcastBrief{Speakers: 2, MinSeconds: 20, MaxSeconds: 30}, Logger: quiet}
		if err := open.Check(); err != nil {
			t.Fatal(err)
		}
		id, err := open.Open(ctx, owner, PodcastRequest{Title: "Mitosis", Source: "Four phases.", Focus: "Keep it <light>."})
		if err != nil {
			t.Fatalf("opening: %v", err)
		}
		var status, title, creator string
		var version, thread int64
		var artifact *int64
		var transcript, failure, spec []byte
		if err := pool.QueryRow(ctx, `SELECT status::text, title, spec_version, thread_id, user_id::text, artifact_id, podcast_transcript, error, spec
			FROM podcast_runs WHERE id = $1`, id).Scan(&status, &title, &version, &thread, &creator, &artifact, &transcript, &failure, &spec); err != nil {
			t.Fatalf("reading the podcast back: %v", err)
		}
		if status != "awaiting_brief" || title != "Mitosis" || version != 1 || thread != 1 || creator != ben || artifact != nil || transcript != nil || failure != nil {
			t.Errorf("row: %s %q v%d thread %d of %s", status, title, version, thread, creator)
		}
		// jsonb keeps a brief's values and not its key order, so the stored
		// brief is compared by what it says.
		language, cast, focus := briefOf(t, spec)
		if language != "en" || strings.Join(cast, "|") != "Host host kokoro:af_heart|Guest guest kokoro:am_adam" || focus != "Keep it <light>." {
			t.Errorf("brief: %s %v focus %v", language, cast, focus)
		}
		var style string
		var shortest, longest, speakers int
		if err := pool.QueryRow(ctx, `SELECT spec ->> 'style', (spec -> 'duration' ->> 'min_seconds')::int, (spec -> 'duration' ->> 'max_seconds')::int, jsonb_array_length(spec -> 'speakers')
			FROM podcast_runs WHERE id = $1 AND spec -> 'speakers' -> 0 -> 'delivery' = 'null'::jsonb AND (spec -> 'speakers' -> 1 ->> 'slot')::int = 1`, id).
			Scan(&style, &shortest, &longest, &speakers); err != nil || style != "conversational" || shortest != 20 || longest != 30 || speakers != 2 {
			t.Errorf("brief: %s %d-%d, %d speakers, %v", style, shortest, longest, speakers, err)
		}
		var source string
		if err := pool.QueryRow(ctx, `SELECT source_content FROM podcast_runs WHERE id = $1`, id).Scan(&source); err != nil || source != "Four phases." {
			t.Errorf("source = %q, %v", source, err)
		}
		// A podcast is opened on a thread of its own account only: Ada
		// naming Ben's thread writes nothing.
		before := count(t, pool, "podcast_runs")
		if _, err := open.Open(ctx, Owner{ThreadID: 1, UserID: ada}, PodcastRequest{Title: "Not hers", Source: "S."}); err == nil || count(t, pool, "podcast_runs") != before {
			t.Errorf("Ada opened a podcast on Ben's thread: %v", err)
		}
		// The next podcast starts from what this account chose last: the
		// brief just stored is one this package's reader understands.
		if _, err := pool.Exec(ctx, `UPDATE podcast_runs SET spec = jsonb_set(jsonb_set(spec, '{language}', '"es"'), '{speakers,0,voice_id}', '"kokoro:ef_dora"') WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		next, err := open.Open(ctx, owner, PodcastRequest{Title: "Meiosis", Source: "Two divisions."})
		if err != nil {
			t.Fatal(err)
		}
		adas, err := open.Open(ctx, Owner{ThreadID: 9, UserID: ada}, PodcastRequest{Title: "Hers", Source: "S."})
		if err != nil {
			t.Fatal(err)
		}
		var adasLanguage string
		if err := pool.QueryRow(ctx, `SELECT spec ->> 'language' FROM podcast_runs WHERE id = $1 AND user_id = $2`, adas, ada).Scan(&adasLanguage); err != nil || adasLanguage != "en" {
			t.Errorf("Ada's first brief took Ben's language: %q, %v", adasLanguage, err)
		}
		var nextLanguage, first, nextFocus string
		if err := pool.QueryRow(ctx, `SELECT spec ->> 'language', spec -> 'speakers' -> 0 ->> 'voice_id', jsonb_typeof(spec -> 'focus') FROM podcast_runs WHERE id = $1`, next).Scan(&nextLanguage, &first, &nextFocus); err != nil {
			t.Fatal(err)
		}
		if nextLanguage != "es" || first != "kokoro:ef_dora" || nextFocus != "null" {
			t.Errorf("the next brief: %s, %s, focus %s", nextLanguage, first, nextFocus)
		}
	})
}
