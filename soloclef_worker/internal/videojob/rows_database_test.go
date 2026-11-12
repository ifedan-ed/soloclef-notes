package videojob

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_studio/artifactsave"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

// queuedVideo writes a job as the API tier's chat does: queued, under the
// first attempt's task id, in a thread of the owner's.
func queuedVideo(t *testing.T, pool *pgxpool.Pool, owner string) (Claimed, int64) {
	t.Helper()
	ctx := context.Background()
	var thread, id int64
	if err := pool.QueryRow(ctx, `INSERT INTO new_chat_threads (user_id, title) VALUES ($1::uuid, 'Heart') RETURNING id`, owner).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]any{"version": 1, "brief": "The heart", "source_references": []string{}, "root_thread_id": thread})
	if err := pool.QueryRow(ctx, `
		INSERT INTO deliverable_jobs (user_id, kind, title, thread_id, tool_call_id, request)
		VALUES ($1::uuid, 'video', 'The heart', $2::bigint, 'call-' || $2::bigint::text, $3) RETURNING id`, owner, thread, request).Scan(&id); err != nil {
		t.Fatal(err)
	}
	task := fmt.Sprintf("deliverable-job:%d:attempt:1", id)
	testdatabase.Exec(t, pool, `UPDATE deliverable_jobs SET celery_task_id = $2 WHERE id = $1`, id, task)
	return Claimed{ID: id, UserID: owner, TaskID: task, Attempt: 1}, thread
}

func row(t *testing.T, pool *pgxpool.Pool, id int64) (status, phase, code string, artifact *int64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		SELECT status::text, COALESCE(phase, ''), COALESCE(failure_code, ''), artifact_id FROM deliverable_jobs WHERE id = $1`, id).
		Scan(&status, &phase, &code, &artifact); err != nil {
		t.Fatal(err)
	}
	return
}

var rules = ClaimRules{StaleAfter: time.Minute, MaxTakeovers: 2}

// The claim runs an attempt once: not for a duplicate delivery while it is
// alive, not for another attempt's message; it takes over an attempt whose
// worker went silent, twice, and then marks it failed.
func TestTheClaimRunsAnAttemptOnce(t *testing.T) {
	pool := testdatabase.Open(t)
	owner := testdatabase.Account(t, pool)
	store := Store{Pool: pool}
	ctx := context.Background()
	job, _ := queuedVideo(t, pool, owner)

	if claim, err := store.Claim(ctx, job.ID, "deliverable-job:1:attempt:9", rules); err != nil || claim.Outcome != claimNothing {
		t.Fatalf("another attempt's message: %+v (%v)", claim, err)
	}
	claim, err := store.Claim(ctx, job.ID, job.TaskID, rules)
	if err != nil || claim.Outcome != claimRun || claim.Job.UserID != owner || claim.Job.Kind != "video" || claim.Job.ThreadID == nil {
		t.Fatalf("claim %+v (%v)", claim, err)
	}
	if status, phase, _, _ := row(t, pool, job.ID); status != "running" || phase != "starting" {
		t.Errorf("claimed row is %s/%s", status, phase)
	}
	if again, _ := store.Claim(ctx, job.ID, job.TaskID, rules); again.Outcome != claimNothing {
		t.Errorf("a duplicate delivery ran the live attempt again: %+v", again)
	}
	for takeover := 1; takeover <= 2; takeover++ {
		testdatabase.Exec(t, pool, `UPDATE deliverable_jobs SET heartbeat_at = now() - interval '2 minutes' WHERE id = $1`, job.ID)
		over, err := store.Claim(ctx, job.ID, job.TaskID, rules)
		if err != nil || over.Outcome != claimRun || over.Job.Takeovers != takeover {
			t.Fatalf("takeover %d: %+v (%v)", takeover, over, err)
		}
	}
	testdatabase.Exec(t, pool, `UPDATE deliverable_jobs SET heartbeat_at = now() - interval '2 minutes' WHERE id = $1`, job.ID)
	if last, _ := store.Claim(ctx, job.ID, job.TaskID, rules); last.Outcome != claimNothing {
		t.Errorf("a third takeover: %+v", last)
	}
	if status, _, code, _ := row(t, pool, job.ID); status != "failed" || code != codeRender {
		t.Errorf("an attempt that kept losing its worker is %s/%s", status, code)
	}

	// A cancel whose worker is gone is finished by the next delivery.
	cancelled, _ := queuedVideo(t, pool, owner)
	testdatabase.Exec(t, pool, `UPDATE deliverable_jobs SET status = 'cancelling', heartbeat_at = now() - interval '2 minutes' WHERE id = $1`, cancelled.ID)
	finish, err := store.Claim(ctx, cancelled.ID, cancelled.TaskID, rules)
	if err != nil || finish.Outcome != claimFinishCancel {
		t.Fatalf("a stale cancel: %+v (%v)", finish, err)
	}
	if err := store.Cancelled(ctx, finish.Job); err != nil {
		t.Fatal(err)
	}
	if status, _, code, _ := row(t, pool, cancelled.ID); status != "cancelled" || code != "cancelled" {
		t.Errorf("finished cancel is %s/%s", status, code)
	}
}

// Every write after the claim is refused once the attempt is no longer
// running under its task id: a cancel, a retry.
func TestAnAttemptsWritesStopWhenItMovesOn(t *testing.T) {
	pool := testdatabase.Open(t)
	owner := testdatabase.Account(t, pool)
	store := Store{Pool: pool}
	ctx := context.Background()
	job, _ := queuedVideo(t, pool, owner)
	claim, _ := store.Claim(ctx, job.ID, job.TaskID, rules)
	job = claim.Job

	if ours, err := store.Heartbeat(ctx, job, "rendering", 65); err != nil || !ours {
		t.Fatalf("heartbeat: %v %v", ours, err)
	}
	// The API's cancel route.
	testdatabase.Exec(t, pool, `UPDATE deliverable_jobs SET status = 'cancelling', phase = 'cancelling' WHERE id = $1 AND status = 'running'`, job.ID)
	if ours, _ := store.Heartbeat(ctx, job, "verifying", 85); ours {
		t.Error("a cancelled attempt's heartbeat was written")
	}
	// A failure that arrives after a cancel was asked for is a cancel.
	if err := store.Fail(ctx, job, codeRender, "boom"); err != nil {
		t.Fatal(err)
	}
	if status, _, code, _ := row(t, pool, job.ID); status != "cancelled" || code != "cancelled" {
		t.Errorf("a failure after a cancel left %s/%s", status, code)
	}

	// The retry route: the next attempt, its own task id. The old attempt's
	// release changes nothing.
	testdatabase.Exec(t, pool, `UPDATE deliverable_jobs SET status = 'queued', attempt_count = 2, celery_task_id = $2 WHERE id = $1`,
		job.ID, fmt.Sprintf("deliverable-job:%d:attempt:2", job.ID))
	if err := store.Release(ctx, job); err != nil {
		t.Fatal(err)
	}
	next, _ := store.Claim(ctx, job.ID, fmt.Sprintf("deliverable-job:%d:attempt:2", job.ID), rules)
	if next.Outcome != claimRun || next.Job.sandboxName() != fmt.Sprintf("deliverable-job-%d-attempt-2", job.ID) {
		t.Fatalf("the retry: %+v", next)
	}
	if err := store.Fail(ctx, job, codeRender, "the old attempt"); err != nil {
		t.Fatal(err)
	}
	if status, _, _, _ := row(t, pool, job.ID); status != "running" {
		t.Errorf("the old attempt's failure was written over the retry: %s", status)
	}
	if err := store.Release(ctx, next.Job); err != nil {
		t.Fatal(err)
	}
	if status, phase, _, _ := row(t, pool, job.ID); status != "queued" || phase != "" {
		t.Errorf("released: %s/%s", status, phase)
	}
}

// The save and a cancel race for the row: whichever takes its lock first
// wins, and the other changes nothing. A video is never kept for a job that
// says cancelled, and a cancel never undoes a kept video.
func TestTheSaveAndACancelCannotBothWin(t *testing.T) {
	pool := testdatabase.Open(t)
	owner := testdatabase.Account(t, pool)
	store := Store{Pool: pool}
	ctx := context.Background()
	artifact := func(id int64) artifactsave.Rows {
		return artifactsave.Rows{ID: id, UserID: owner, ToolCallID: "call", Title: "The heart", Format: "video", Markdown: "# The heart\n",
			Metadata: []byte(`{"verification": {"verified": true, "reason": null}}`),
			Primary: artifactsave.StoredFile{Backend: "local", Key: fmt.Sprintf("artifacts/%s/%d/primary/x.mp4", owner, id), Filename: "The heart.mp4",
				MimeType: "video/mp4", Size: 10, SHA256: strings.Repeat("a", 64)}}
	}

	// The cancel first: nothing is written.
	first, _ := queuedVideo(t, pool, owner)
	claim, _ := store.Claim(ctx, first.ID, first.TaskID, rules)
	testdatabase.Exec(t, pool, `UPDATE deliverable_jobs SET status = 'cancelling' WHERE id = $1 AND status = 'running'`, first.ID)
	id, _ := store.ReserveArtifactID(ctx)
	if _, delivered, err := store.Deliver(ctx, claim.Job, artifact(id)); err != nil || delivered {
		t.Fatalf("delivered over a cancel: %v (%v)", delivered, err)
	}
	var artifacts int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM artifacts WHERE user_id = $1::uuid`, owner).Scan(&artifacts)
	if artifacts != 0 {
		t.Errorf("%d artifacts were written for a cancelled video", artifacts)
	}

	// The save first, holding the lock while a cancel waits on it: the
	// cancel then finds the job ready and leaves it.
	second, thread := queuedVideo(t, pool, owner)
	claim, _ = store.Claim(ctx, second.ID, second.TaskID, rules)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM deliverable_jobs WHERE id = $1 FOR UPDATE`, second.ID); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	wait.Add(1)
	var cancelled int64
	go func() {
		defer wait.Done()
		tag, _ := pool.Exec(ctx, `UPDATE deliverable_jobs SET status = 'cancelling' WHERE id = $1 AND status = 'running' AND artifact_id IS NULL`, second.ID)
		cancelled = tag.RowsAffected()
	}()
	time.Sleep(100 * time.Millisecond)
	// Released, and the save takes the lock before the cancel's statement
	// re-reads the row (it queued behind this one).
	_ = tx.Rollback(ctx)
	id, _ = store.ReserveArtifactID(ctx)
	_, delivered, err := store.Deliver(ctx, claim.Job, artifact(id))
	wait.Wait()
	status, _, _, kept := row(t, pool, second.ID)
	switch {
	case err != nil:
		t.Fatal(err)
	case delivered && (status != "ready" || kept == nil || *kept != id || cancelled != 0):
		t.Errorf("delivered, then the row is %s with artifact %v and the cancel changed %d rows", status, kept, cancelled)
	case !delivered && (status != "cancelling" || kept != nil):
		t.Errorf("not delivered, and the row is %s with artifact %v", status, kept)
	}
	if delivered {
		var format string
		var threadID *int64
		_ = pool.QueryRow(ctx, `SELECT format, thread_id FROM artifacts WHERE id = $1 AND user_id = $2::uuid`, id, owner).Scan(&format, &threadID)
		if format != "video" || threadID == nil || *threadID != thread {
			t.Errorf("the artifact is %q in thread %v", format, threadID)
		}
	}
}

// A cancel during the render, against the real rows: the job stops, the
// row says cancelled, and no artifact, document or file is left.
func TestACancelDuringTheRenderIsNotOverwrittenInTheDatabase(t *testing.T) {
	pool := testdatabase.Open(t)
	owner := testdatabase.Account(t, pool)
	ctx := context.Background()
	job, _ := queuedVideo(t, pool, owner)

	r := newRig(t, draftReply("One.", "Two."))
	r.job.Rows = Store{Pool: pool}
	r.job.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	rendering := make(chan struct{})
	r.box.render = func(ctx context.Context) {
		close(rendering)
		<-ctx.Done()
	}
	go func() {
		<-rendering
		// The API's cancel route, while the render runs.
		_, _ = pool.Exec(context.Background(), `UPDATE deliverable_jobs SET status = 'cancelling', phase = 'cancelling', cancel_requested_at = now() WHERE id = $1 AND status = 'running'`, job.ID)
	}()
	message := &celery.Message{Task: Task, ID: job.TaskID, Kwargs: map[string]any{"job_id": float64(job.ID)}}
	if err := r.job.Run(ctx, message); err != nil {
		t.Fatal(err)
	}
	status, _, code, artifact := row(t, pool, job.ID)
	if status != "cancelled" || code != "cancelled" || artifact != nil {
		t.Fatalf("row %s/%s, artifact %v", status, code, artifact)
	}
	var artifacts, documents int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM artifacts WHERE user_id = $1::uuid`, owner).Scan(&artifacts)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM documents WHERE user_id = $1::uuid`, owner).Scan(&documents)
	if artifacts != 0 || documents != 0 || len(r.storedObjects(t)) != 0 {
		t.Errorf("left %d artifacts, %d documents, files %v", artifacts, documents, r.storedObjects(t))
	}
}

// The whole save against the real rows: the artifact, its document, its
// file row, and the job ready and pointing at it, in one commit.
func TestAVideoIsSavedInTheDatabase(t *testing.T) {
	pool := testdatabase.Open(t)
	owner := testdatabase.Account(t, pool)
	ctx := context.Background()
	job, thread := queuedVideo(t, pool, owner)
	r := newRig(t, draftReply("One.", "Two."))
	r.job.Rows = Store{Pool: pool}
	if err := r.job.Run(ctx, &celery.Message{Task: Task, ID: job.TaskID, Kwargs: map[string]any{"job_id": float64(job.ID)}}); err != nil {
		t.Fatal(err)
	}
	status, phase, _, artifact := row(t, pool, job.ID)
	if status != "ready" || phase != "ready" || artifact == nil {
		t.Fatalf("row %s/%s artifact %v", status, phase, artifact)
	}
	var (
		format, role, mime, key, docPath string
		size                             int64
		threadID                         *int64
		slides                           int
	)
	err := pool.QueryRow(ctx, `
		SELECT a.format, a.thread_id, f.role::text, f.mime_type, f.storage_key, f.size_bytes, d.path,
		       jsonb_array_length(a.metadata -> 'slides')
		FROM artifacts a JOIN artifact_files f ON f.artifact_id = a.id JOIN documents d ON d.id = a.document_id
		WHERE a.id = $1 AND a.user_id = $2::uuid`, *artifact, owner).Scan(&format, &threadID, &role, &mime, &key, &size, &docPath, &slides)
	if err != nil {
		t.Fatal(err)
	}
	if format != "video" || threadID == nil || *threadID != thread || role != "primary" || mime != "video/mp4" ||
		!strings.HasPrefix(key, fmt.Sprintf("artifacts/%s/%d/primary/", owner, *artifact)) || size != int64(len("an mp4 of the scenes")) ||
		docPath != "/documents/The heart.md" || slides != 2 {
		t.Errorf("saved %s %v %s %s %s %d %s %d", format, threadID, role, mime, key, size, docPath, slides)
	}
}

// With the render switched off, a queued video is told so at once; a
// cancelled one stays cancelled.
func TestASwitchedOffRenderRefusesAtOnce(t *testing.T) {
	pool := testdatabase.Open(t)
	owner := testdatabase.Account(t, pool)
	ctx := context.Background()
	queued, _ := queuedVideo(t, pool, owner)
	cancelled, _ := queuedVideo(t, pool, owner)
	testdatabase.Exec(t, pool, `UPDATE deliverable_jobs SET status = 'cancelled' WHERE id = $1`, cancelled.ID)
	for _, id := range []int64{queued.ID, cancelled.ID} {
		if err := (Refusal{Pool: pool}).Run(ctx, &celery.Message{Task: Task, Kwargs: map[string]any{"job_id": float64(id)}}); err != nil {
			t.Fatal(err)
		}
	}
	if status, _, code, _ := row(t, pool, queued.ID); status != "failed" || code != codeRender {
		t.Errorf("queued became %s/%s", status, code)
	}
	if status, _, _, _ := row(t, pool, cancelled.ID); status != "cancelled" {
		t.Errorf("cancelled became %s", status)
	}
}
