package deliverables

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// TestPicturesAndVideosOverARealDatabase runs this file's statements against
// the real schema and its constraints: a queued video as the render worker
// will find it, and the two picture formats read back through the package
// that serves them to the page. It needs the throwaway Postgres
// database_test.go describes (databaseVariable), and truncates what it
// seeds.
func TestPicturesAndVideosOverARealDatabase(t *testing.T) {
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
	// The shared seed, a thread of Cat's and a second one of hers.
	if _, err := pool.Exec(ctx, seed+`
INSERT INTO new_chat_threads (id, title, user_id) VALUES
  (3, 'Cat''s chat', '`+cat+`'),
  (2, 'Another chat', '`+cat+`');
`); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	store := VideoStore{Pool: pool}
	request := []byte(`{"version":1,"brief":"A brief.","source_references":[],"root_thread_id":1}`)

	t.Run("a job cannot be queued on another account's thread", func(t *testing.T) {
		// Thread 1 is Ben's. Ada, Cat and an account that does not exist
		// each name it: no job, whoever asks.
		for _, who := range []string{ada, cat, "dddddddd-0000-4000-8000-000000000004"} {
			if _, err := store.CreateVideo(ctx, Owner{ThreadID: 1, UserID: who}, "call-denied", "T", request); err == nil {
				t.Errorf("%s queued a video on Ben's thread", who)
			}
		}
		if n := count(t, pool, "deliverable_jobs"); n != 0 {
			t.Fatalf("%d jobs were written", n)
		}
	})

	t.Run("a queued video is the row the worker claims", func(t *testing.T) {
		owner := Owner{ThreadID: 3, UserID: cat}
		queued, err := store.CreateVideo(ctx, owner, "call-1", "Mitosis explained", request)
		if err != nil || !queued.Created || queued.JobID < 1 || queued.TaskID != videoTaskID(queued.JobID) {
			t.Fatalf("queued = %+v, %v", queued, err)
		}
		var kind, title, status, callID, stored, checkpoint, creator string
		var thread, attempts, progress int64
		var artifact *int64
		if err := pool.QueryRow(ctx, `SELECT kind, title, status::text, tool_call_id, request::text, checkpoint::text, user_id::text,
				thread_id, attempt_count, progress, artifact_id FROM deliverable_jobs WHERE id = $1`, queued.JobID).
			Scan(&kind, &title, &status, &callID, &stored, &checkpoint, &creator, &thread, &attempts, &progress, &artifact); err != nil {
			t.Fatal(err)
		}
		if kind != "video" || title != "Mitosis explained" || status != "queued" || callID != "call-1" || checkpoint != "{}" || creator != cat ||
			thread != 3 || attempts != 1 || progress != 0 || artifact != nil {
			t.Errorf("row: %s %q %s %s %s by %s, thread %d, attempt %d, progress %d", kind, title, status, callID, checkpoint, creator, thread, attempts, progress)
		}
		// jsonb keeps the fields, not the bytes; the worker reads fields.
		if !strings.Contains(stored, `"version": 1`) || !strings.Contains(stored, `"root_thread_id": 1`) || !strings.Contains(stored, `"source_references": []`) {
			t.Errorf("request = %s", stored)
		}

		// A second run of the same call is the same job, and writes nothing.
		again, err := store.CreateVideo(ctx, owner, "call-1", "Mitosis explained", request)
		if err != nil || again.Created || again.JobID != queued.JobID || again.TaskID != queued.TaskID || count(t, pool, "deliverable_jobs") != 1 {
			t.Fatalf("a second run: %+v, %v", again, err)
		}
		// The same call id from another conversation is not handed this
		// one's job.
		if _, err := store.CreateVideo(ctx, Owner{ThreadID: 2, UserID: cat}, "call-1", "Other", request); !errors.Is(err, errForeignVideo) {
			t.Fatalf("another thread with the same call id: %v", err)
		}

		// The worker's claim (app/deliverables/jobs/service.py,
		// claim_deliverable_job) as SQL: it takes the job only for the
		// message whose id the row names.
		const claim = `UPDATE deliverable_jobs SET status = 'running', phase = 'starting', claimed_at = now(), heartbeat_at = now()
			WHERE id = $1 AND status = 'queued' AND artifact_id IS NULL AND celery_task_id = $2`
		if tag, err := pool.Exec(ctx, claim, queued.JobID, "some-random-uuid"); err != nil || tag.RowsAffected() != 0 {
			t.Fatalf("a message under another id claimed the job: %v", err)
		}
		if tag, err := pool.Exec(ctx, claim, queued.JobID, queued.TaskID); err != nil || tag.RowsAffected() != 1 {
			t.Fatalf("the job's own message did not claim it: %v", err)
		}

		// The same call id is another account's own job, not this one:
		// the idempotency key is per account.
		bens, err := store.CreateVideo(ctx, Owner{ThreadID: 1, UserID: ben}, "call-1", "Ben's", []byte(`{"version":1,"brief":"B.","source_references":[],"root_thread_id":1}`))
		if err != nil || !bens.Created || bens.JobID == queued.JobID {
			t.Fatalf("Ben's job under the same call id: %+v, %v", bens, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM deliverable_jobs WHERE id = $1`, bens.JobID); err != nil {
			t.Fatal(err)
		}

		// A thread that is not there: an error, and no row.
		if _, err := store.CreateVideo(ctx, Owner{ThreadID: 99, UserID: cat}, "call-2", "T", request); err == nil {
			t.Error("a job was queued for a thread that does not exist")
		}
		if count(t, pool, "deliverable_jobs") != 1 {
			t.Error("a refused job left a row")
		}
	})

	local, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saver := Saver{
		Rows: Store{Pool: pool}, Files: local, Logger: discardLogger(),
		NewID: func() string { return "00000000000000000000000000000abc" },
		Index: func(context.Context, int64, string) error { return nil },
	}
	owner := Owner{ThreadID: 1, UserID: ben}
	read := savedRows{Pool: pool}

	t.Run("an image is rows and a file the artifact routes read", func(t *testing.T) {
		data := picture(64, 64, color.NRGBA{G: 200, A: 255})
		saved, err := saver.SaveImage(ctx, owner, "d1/g1", "A green square", "gpt-image-test", GeneratedImage{Data: data, MimeType: "image/png", Extension: "png", RevisedPrompt: "A flat green square."})
		if err != nil {
			t.Fatalf("saving: %v", err)
		}
		manifest, err := read.ManifestOf(ctx, ben, saved.ArtifactID)
		if err != nil || manifest == nil || manifest.Format != "image" || manifest.Title != "A green square" || len(manifest.Files) != 1 {
			t.Fatalf("manifest = %+v, %v", manifest, err)
		}
		file := manifest.Files[0]
		if file.Filename != "a-green-square.png" || file.MimeType != "image/png" || file.Size != int64(len(data)) ||
			file.Key != "artifacts/"+ben+"/"+strconv.FormatInt(saved.ArtifactID, 10)+"/primary/00000000000000000000000000000abc.png" {
			t.Errorf("file = %+v", file)
		}
		if stored := storedFile(t, local, file.Key); !bytes.Equal(stored, data) {
			t.Errorf("the stored file is %d bytes, the image %d", len(stored), len(data))
		}
		var model, revised string
		if err := pool.QueryRow(ctx, `SELECT metadata ->> 'model', metadata ->> 'revised_prompt' FROM artifacts WHERE id = $1`, saved.ArtifactID).Scan(&model, &revised); err != nil || model != "gpt-image-test" || revised != "A flat green square." {
			t.Errorf("metadata: %s, %s, %v", model, revised, err)
		}
	})

	t.Run("an infographic keeps its infographicPlan, its PNG and what the check found", func(t *testing.T) {
		checked, err := NewInfographic("Photosynthesis", infographicPlan)
		if err != nil {
			t.Fatal(err)
		}
		data, width, height, err := normalisedPNG(picture(640, 400, color.NRGBA{B: 200, A: 128}))
		if err != nil {
			t.Fatal(err)
		}
		score := 0.5
		check := TextCheck{Score: &score, Checked: 4, Missing: []string{"Calvin cycle"}, Garbled: []string{`"95%" (reads "59%")`}}
		saved, err := saver.SaveInfographic(ctx, owner, "d2/s1", checked, InfographicRecord{
			RequestedStyle: "auto", Style: "sketch-note", Model: "gpt-image-test", Width: width, Height: height, Attempts: 2, Check: check,
		}, data)
		if err != nil {
			t.Fatalf("saving: %v", err)
		}
		manifest, err := read.ManifestOf(ctx, ben, saved.ArtifactID)
		if err != nil || manifest == nil || manifest.Format != "infographic" || manifest.Markdown != checked.Markdown || len(manifest.Files) != 1 ||
			manifest.Files[0].Filename != "photosynthesis.png" || manifest.Files[0].MimeType != "image/png" {
			t.Fatalf("manifest = %+v, %v", manifest, err)
		}
		var verified, resolved, warning, missing string
		if err := pool.QueryRow(ctx, `SELECT metadata -> 'verification' ->> 'verified', metadata -> 'generation' ->> 'resolved_style_id',
				metadata -> 'verification' -> 'warnings' ->> 0, metadata -> 'generation' -> 'text_check' -> 'missing' ->> 0
			FROM artifacts WHERE id = $1`, saved.ArtifactID).Scan(&verified, &resolved, &warning, &missing); err != nil {
			t.Fatal(err)
		}
		if verified != "true" || resolved != "sketch-note" || missing != "Calvin cycle" ||
			warning != `Only 50% of the planned text reads correctly in the image after one regeneration. Check: Calvin cycle; "95%" (reads "59%")` {
			t.Errorf("metadata: verified %s, style %s, warning %q, missing %q", verified, resolved, warning, missing)
		}
	})
}
