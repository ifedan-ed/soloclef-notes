package studiogenerate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// These tests are the jobs' side of a deploy: a process told to stop, a
// process that died, and the process that starts next, each a Runner over
// the same database. They run against the throwaway Postgres the other
// database tests use (SOLOCLEF_API_TEST_STUDIOGENERATE_DATABASE).

// jobState is what a test reads back about one job.
type jobState struct {
	status, phase string
	attempt       int64
	failureCode   *string
	hasTask       bool
	artifactID    *int64
}

func (h *harness) job(id int64) jobState {
	h.t.Helper()
	var state jobState
	err := h.pool.QueryRow(context.Background(), `
		SELECT status::text, COALESCE(phase, ''), attempt_count, failure_code, request ->> 'task' IS NOT NULL, artifact_id
		FROM deliverable_jobs WHERE id = $1`, id).Scan(
		&state.status, &state.phase, &state.attempt, &state.failureCode, &state.hasTask, &state.artifactID)
	if err != nil {
		h.t.Fatal(err)
	}
	return state
}

// queueMindMap asks for a mind map through the route, as the person does,
// and answers its job's id.
func (h *harness) queueMindMap(requestID string) int64 {
	h.t.Helper()
	status, body := h.as(alice, "POST", generate, `{"kind":"mindmap","request_id":"`+requestID+`"}`)
	if status != 201 {
		h.t.Fatalf("queueing a mind map: %d %s", status, body)
	}
	var id int64
	if err := h.pool.QueryRow(context.Background(),
		`SELECT id FROM deliverable_jobs WHERE tool_call_id = $1`, "notebook-studio:10:"+requestID).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

// seedJob writes an artifact job the way a process that then died left it:
// its hidden thread, its stored task, its state. It answers the job's id and
// its thread's.
func (h *harness) seedJob(status string, attempt int, silentFor time.Duration) (jobID, threadID int64) {
	h.t.Helper()
	ctx := context.Background()
	q := New(h.pool)
	threadID, err := q.CreateStudioThread(ctx, CreateStudioThreadParams{Title: "Tides", NotebookID: 10, UserID: alice})
	if err != nil {
		h.t.Fatal(err)
	}
	stored, err := encodeTask("mindmap", "mindmap", Job{Task: "Kind of artifact: mindmap.\n\n## Source: Moon\n\nThe moon pulls the sea."})
	if err != nil {
		h.t.Fatal(err)
	}
	err = h.pool.QueryRow(ctx, `
		INSERT INTO deliverable_jobs (user_id, kind, title, thread_id, tool_call_id, request, status, phase, attempt_count,
		                              claimed_at, heartbeat_at, updated_at)
		VALUES ($1, 'artifact', 'Mind Map: Tides', $2, $3, $4, $5::deliverable_job_status, 'generating', $6,
		        now() - $7::interval, now() - $7::interval, now() - $7::interval)
		RETURNING id`, alice, threadID, "seeded-"+strconv.FormatInt(threadID, 10), stored, status, attempt,
		fmt.Sprintf("%d seconds", int(silentFor.Seconds()))).Scan(&jobID)
	if err != nil {
		h.t.Fatal(err)
	}
	return jobID, threadID
}

func TestADeployWaitsForARunAndLeavesNewJobsForTheNextProcess(t *testing.T) {
	h := newBareHarness(t, false)
	hold := make(chan struct{})
	h.model.hold = hold

	// The process being deployed over, with a run in progress.
	accepting, stopAccepting := context.WithCancel(h.lifetime)
	lifetime, endLifetime := context.WithCancel(h.lifetime)
	defer endLifetime()
	old := h.runnerWith(accepting, lifetime)
	running := h.queueMindMap("before-the-deploy")
	h.until("the run is claimed", "deliverable_jobs WHERE status = 'running' AND id = "+strconv.FormatInt(running, 10))

	// The signal: no more claims, and the run goes on.
	stopAccepting()
	waiting := h.queueMindMap("during-the-drain")
	select {
	case <-old.Idle():
		t.Fatal("the runner says it is idle with a run in progress")
	case <-time.After(100 * time.Millisecond):
	}
	if state := h.job(waiting); state.status != "queued" {
		t.Fatalf("a job queued during the drain was taken by the stopping process: %+v", state)
	}

	// The run finishes inside the drain: ready, and the stopping process
	// is then idle.
	close(hold)
	h.until("the run finishes", "deliverable_jobs WHERE status = 'ready' AND id = "+strconv.FormatInt(running, 10))
	select {
	case <-old.Idle():
	case <-time.After(5 * time.Second):
		t.Fatal("the runner did not become idle once its run had finished")
	}
	endLifetime()
	old.Wait()
	if state := h.job(running); state.attempt != 1 || state.hasTask {
		t.Errorf("a run that finished in the drain: %+v (one attempt, its task gone)", state)
	}

	// The next process takes up what was queued meanwhile, at once.
	h.runnerWith(h.lifetime, h.lifetime)
	h.until("the next process makes the waiting job", "deliverable_jobs WHERE status = 'ready' AND id = "+strconv.FormatInt(waiting, 10))
	if h.count("artifacts") != 2 {
		t.Errorf("%d artifacts for two jobs", h.count("artifacts"))
	}
}

func TestARunCutOffByTheDrainIsMadeAgainByTheNextProcess(t *testing.T) {
	h := newBareHarness(t, false)
	h.model.block = true

	accepting, stopAccepting := context.WithCancel(h.lifetime)
	lifetime, endLifetime := context.WithCancel(h.lifetime)
	old := h.runnerWith(accepting, lifetime)
	id := h.queueMindMap("cut-off")
	h.until("the run is claimed", "deliverable_jobs WHERE status = 'running' AND id = "+strconv.FormatInt(id, 10))
	var threadID int64
	if err := h.pool.QueryRow(context.Background(), `SELECT thread_id FROM deliverable_jobs WHERE id = $1`, id).Scan(&threadID); err != nil {
		t.Fatal(err)
	}

	// The drain's bound passes with the run still going.
	stopAccepting()
	endLifetime()
	old.Wait()
	state := h.job(id)
	if state.status != "queued" || state.phase != "queued" || state.attempt != 2 || !state.hasTask || state.failureCode != nil {
		t.Fatalf("a run cut off by the drain should be queued as its second attempt with its task: %+v", state)
	}
	if h.count("notifications") != 0 {
		t.Error("the owner was told about a job that is still being made")
	}

	// The next process makes it, cleanly: the first attempt's sandbox is
	// removed, and it is made in a conversation of its own.
	h.model.block = false
	stale := checkpoint.Checkpoint{Thread: attemptConversation(id, 1), Seq: 1, Status: checkpoint.Running, Node: "model"}
	if err := h.specialist.Store.Save(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	h.runnerWith(h.lifetime, h.lifetime)
	h.until("the second attempt finishes", "deliverable_jobs WHERE status = 'ready' AND attempt_count = 2 AND id = "+strconv.FormatInt(id, 10))
	// Three releases of the job's sandbox: the first attempt's as it was
	// cut off (one model call made), the second's fresh start before it
	// asks the model anything, and the second's at its end.
	released := h.sandboxes.released()
	if len(released) != 3 || released[0].calls != 1 || released[1].calls != 1 || released[2].calls <= 1 {
		t.Errorf("sandbox releases %+v: want the cut-off attempt's, a fresh one before attempt 2, and attempt 2's own", released)
	}
	for _, each := range released {
		if each.thread != threadID {
			t.Errorf("a sandbox of thread %d was released, not the job's %d", each.thread, threadID)
		}
	}
	if _, err := h.specialist.Store.Load(context.Background(), attemptConversation(id, 1)); !errors.Is(err, checkpoint.ErrNotFound) {
		t.Errorf("the first attempt's conversation is still there: %v", err)
	}
	if h.count("artifacts") != 1 || h.job(id).hasTask {
		t.Errorf("%d artifacts; task kept: %v", h.count("artifacts"), h.job(id).hasTask)
	}
	h.until("its owner is told it is ready", `notifications WHERE type = 'studio_output' AND metadata ->> 'status' = 'completed'`)
}

func TestAJobWhoseProcessDiedIsTakenUpAtStart(t *testing.T) {
	h := newBareHarness(t, false)
	dead, _ := h.seedJob("running", 1, 10*time.Minute)
	alive, _ := h.seedJob("running", 1, 0)
	stopping, _ := h.seedJob("cancelling", 1, 10*time.Minute)

	h.runnerWith(h.lifetime, h.lifetime)
	h.until("the dead process's job is made", "deliverable_jobs WHERE status = 'ready' AND attempt_count = 2 AND id = "+strconv.FormatInt(dead, 10))
	if state := h.job(alive); state.status != "running" || state.attempt != 1 {
		t.Errorf("a job whose process is alive was taken from it: %+v", state)
	}
	if state := h.job(stopping); state.status != "cancelled" || state.attempt != 1 {
		t.Errorf("a job the person was stopping should be cancelled, not made again: %+v", state)
	}
}

func TestAJobCutOffOnEveryAttemptFailsAsInterrupted(t *testing.T) {
	h := newBareHarness(t, false)
	ctx := context.Background()
	q := New(h.pool)
	now := time.Now().UTC()

	// Died on its last attempt: failed, and its owner told.
	last, _ := h.seedJob("running", maxAttempts, 10*time.Minute)
	// Died with attempts left: queued again.
	early, _ := h.seedJob("running", maxAttempts-1, 10*time.Minute)
	swept, err := q.RequeueAbandonedJobs(ctx, RequeueAbandonedJobsParams{
		Reason: abandonedReason, Now: now, MaxAttempts: maxAttempts, SilentSince: now.Add(-abandonedAfter), Most: sweepBatch,
	})
	if err != nil || len(swept) != 2 {
		t.Fatalf("the sweep took %d jobs, %v", len(swept), err)
	}
	if state := h.job(last); state.status != "failed" || state.failureCode == nil || *state.failureCode != codeInterrupted ||
		state.attempt != maxAttempts || state.hasTask {
		t.Errorf("a job out of attempts: %+v", state)
	}
	if state := h.job(early); state.status != "queued" || state.attempt != maxAttempts {
		t.Errorf("a job with an attempt left: %+v", state)
	}

	// The drain's release holds to the same limit.
	cutLast, _ := h.seedJob("running", maxAttempts, 0)
	released, err := q.ReleaseJob(ctx, ReleaseJobParams{Reason: shutdownReason, Now: now, MaxAttempts: maxAttempts, ID: cutLast, UserID: alice, Attempt: maxAttempts})
	if err != nil || released.Status != "failed" {
		t.Errorf("a release on the last attempt: %+v, %v", released, err)
	}

	// A runner reports the sweep's failure to the owner, in the words the
	// panel uses for an interruption.
	h.runnerWith(h.lifetime, h.lifetime)
	diedAgain, _ := h.seedJob("running", maxAttempts, 10*time.Minute)
	h.runnerWith(h.lifetime, h.lifetime)
	h.until("the failure is told", `notifications WHERE type = 'studio_output' AND metadata ->> 'status' = 'failed'`)
	if state := h.job(diedAgain); state.status != "failed" {
		t.Errorf("the last attempt of a job whose process died again: %+v", state)
	}
}

func TestTwoReplicasNeverClaimTheSameJob(t *testing.T) {
	h := newBareHarness(t, false)
	const jobs, replicas = 40, 4
	for i := 0; i < jobs; i++ {
		h.seedJob("queued", 1, 0)
	}
	// Each replica claims in small batches, all at once, until the queue
	// is empty. Every job must be claimed exactly once.
	var (
		mu      sync.Mutex
		claimed []int64
		wait    sync.WaitGroup
	)
	for r := 0; r < replicas; r++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for {
				// A share as large as the queue: this is about the
				// claim's exclusivity, not the share (fair_share_database_test.go).
				rows, err := claimFairly(context.Background(), h.pool, time.Now().UTC(), jobs, 3)
				if err != nil {
					t.Error(err)
					return
				}
				if len(rows) == 0 {
					return
				}
				mu.Lock()
				for _, row := range rows {
					claimed = append(claimed, row.ID)
				}
				mu.Unlock()
			}
		}()
	}
	wait.Wait()
	sort.Slice(claimed, func(i, j int) bool { return claimed[i] < claimed[j] })
	for i := 1; i < len(claimed); i++ {
		if claimed[i] == claimed[i-1] {
			t.Fatalf("job %d was claimed twice", claimed[i])
		}
	}
	if len(claimed) != jobs || h.count("deliverable_jobs WHERE status = 'running'") != jobs {
		t.Errorf("%d claims for %d jobs", len(claimed), jobs)
	}

	// And two replicas sweeping the same dead jobs at once advance each one
	// attempt, not two.
	testpostgres.Exec(t, h.pool, `UPDATE deliverable_jobs SET heartbeat_at = now() - interval '10 minutes'`)
	var sweeps sync.WaitGroup
	for r := 0; r < replicas; r++ {
		sweeps.Add(1)
		go func() {
			defer sweeps.Done()
			now := time.Now().UTC()
			if _, err := New(h.pool).RequeueAbandonedJobs(context.Background(), RequeueAbandonedJobsParams{
				Reason: abandonedReason, Now: now, MaxAttempts: maxAttempts, SilentSince: now.Add(-abandonedAfter), Most: sweepBatch,
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	sweeps.Wait()
	if n := h.count("deliverable_jobs WHERE status = 'queued' AND attempt_count = 2"); n != jobs {
		t.Errorf("%d of %d jobs were queued again as their second attempt", n, jobs)
	}
}

func TestASupersededAttemptWritesNothing(t *testing.T) {
	h := newBareHarness(t, false)
	ctx := context.Background()
	q := New(h.pool)
	id, _ := h.seedJob("queued", 1, 0)
	if rows, err := claimFairly(ctx, h.pool, time.Now().UTC(), runsPerAccount, 1); err != nil || len(rows) != 1 {
		t.Fatalf("claim: %d rows, %v", len(rows), err)
	}
	// Its process goes silent, and another's sweep takes the job up.
	testpostgres.Exec(t, h.pool, `UPDATE deliverable_jobs SET heartbeat_at = now() - interval '10 minutes'`)
	now := time.Now().UTC()
	if _, err := q.RequeueAbandonedJobs(ctx, RequeueAbandonedJobsParams{
		Reason: abandonedReason, Now: now, MaxAttempts: maxAttempts, SilentSince: now.Add(-abandonedAfter), Most: sweepBatch,
	}); err != nil {
		t.Fatal(err)
	}
	// The first process comes back: its heartbeat, ending and release are
	// all refused.
	if _, err := q.Heartbeat(ctx, HeartbeatParams{Now: now, ID: id, UserID: alice, Attempt: 1}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("a superseded attempt's heartbeat: %v", err)
	}
	phase := "ready"
	if written, err := q.FinishJob(ctx, FinishJobParams{Status: DeliverableJobStatusReady, Phase: &phase, Now: now, ID: id, UserID: alice, Attempt: 1}); err != nil || written != 0 {
		t.Errorf("a superseded attempt's ending wrote %d rows, %v", written, err)
	}
	if _, err := q.ReleaseJob(ctx, ReleaseJobParams{Reason: shutdownReason, Now: now, MaxAttempts: maxAttempts, ID: id, UserID: alice, Attempt: 1}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("a superseded attempt's release: %v", err)
	}
	if state := h.job(id); state.status != "queued" || state.attempt != 2 || !state.hasTask {
		t.Errorf("the job after its superseded attempt came back: %+v", state)
	}
}

func TestAnArtifactSavedBeforeTheCutOffIsNotMadeTwice(t *testing.T) {
	h := newBareHarness(t, false)
	id, threadID := h.seedJob("running", 1, 10*time.Minute)
	// The dead attempt had saved its artifact and died before it could say
	// so on the job.
	var artifactID int64
	if err := h.pool.QueryRow(context.Background(), `
		WITH document AS (
			INSERT INTO documents (user_id, title, document_type, content, content_hash, status)
			VALUES ($1, 'Tides', 'NOTE', '# Tides', 'saved-before', '{"state":"ready"}') RETURNING id
		)
		INSERT INTO artifacts (user_id, thread_id, document_id, format)
		SELECT $1, $2, document.id, 'mindmap' FROM document RETURNING id`, alice, threadID).Scan(&artifactID); err != nil {
		t.Fatal(err)
	}

	h.runnerWith(h.lifetime, h.lifetime)
	h.until("the job is finished with the saved artifact", "deliverable_jobs WHERE status = 'ready' AND id = "+strconv.FormatInt(id, 10))
	if state := h.job(id); state.artifactID == nil || *state.artifactID != artifactID {
		t.Errorf("the job should point at the artifact already saved: %+v", state)
	}
	if h.count("artifacts") != 1 || h.model.calls() != 0 {
		t.Errorf("%d artifacts and %d model calls: the artifact was made again", h.count("artifacts"), h.model.calls())
	}
}

func TestAJobQueuedWithoutATaskIsClosedNotClaimed(t *testing.T) {
	h := newBareHarness(t, false)
	// As the version before this one queued a job: the task in memory only.
	testpostgres.Exec(t, h.pool, `
INSERT INTO new_chat_threads (id, user_id, title, notebook_id, source) VALUES (500, '`+alice+`', 'Old', 10, 'artifact_api');
INSERT INTO deliverable_jobs (id, user_id, kind, title, thread_id, tool_call_id, request, status, phase, heartbeat_at, updated_at) VALUES
 (700, '`+alice+`', 'artifact', 'Old', 500, 'old', '{"version":1,"output":"mindmap","format":"mindmap"}', 'queued', 'queued',
  now() - interval '10 minutes', now() - interval '10 minutes')`)
	h.runnerWith(h.lifetime, h.lifetime)
	h.until("the job is closed", "deliverable_jobs WHERE id = 700 AND status = 'failed' AND failure_code = 'interrupted'")
	if h.model.calls() != 0 {
		t.Error("a job with no task was run")
	}
}

// fullSandboxes is an account whose sandboxes are all taken: a bench asked
// for says the run is waiting, and waits until the run is stopped.
type fullSandboxes struct{ waiting chan int64 }

func (fullSandboxes) Check() error { return nil }
func (f fullSandboxes) Bench(ctx context.Context, owner deliverables.Owner, wait deliverables.SandboxWait) (deliverables.Bench, error) {
	if wait.Waiting != nil {
		wait.Waiting(ctx, true)
	}
	select {
	case f.waiting <- owner.ThreadID:
	default:
	}
	<-ctx.Done()
	if wait.Waiting != nil {
		wait.Waiting(context.WithoutCancel(ctx), false)
	}
	return nil, ctx.Err()
}
func (fullSandboxes) Release(context.Context, deliverables.Owner) error { return nil }

// A job waiting for a sandbox is a run in progress: it beats, so no sweep
// takes it for dead; the drain waits for it; at the drain's end it is given
// back as its next attempt with its phase queued (not left saying it waits)
// and its sandbox request let go; and the next process asks afresh.
func TestAJobWaitingForASandboxSurvivesADeploy(t *testing.T) {
	h := newBareHarness(t, false)
	full := fullSandboxes{waiting: make(chan int64, 4)}
	h.sandboxes.Inner, h.models = full, boasterModels{}
	id, threadID := h.seedJob("queued", 1, 0)

	accepting, stopAccepting := context.WithCancel(h.lifetime)
	lifetime, endLifetime := context.WithCancel(h.lifetime)
	old := h.runnerWith(accepting, lifetime)
	h.until("the job waits for a sandbox", "deliverable_jobs WHERE phase = 'waiting_for_sandbox' AND status = 'running' AND id = "+strconv.FormatInt(id, 10))

	// Its heartbeat goes on while it waits: a sweep finds nothing silent.
	time.Sleep(200 * time.Millisecond)
	now := time.Now().UTC()
	swept, err := New(h.pool).RequeueAbandonedJobs(context.Background(), RequeueAbandonedJobsParams{
		Reason: abandonedReason, Now: now, MaxAttempts: maxAttempts, SilentSince: now.Add(-100 * time.Millisecond), Most: sweepBatch,
	})
	if err != nil || len(swept) != 0 {
		t.Fatalf("a sweep took a job that is waiting for a sandbox: %+v, %v", swept, err)
	}

	// The deploy: the drain waits for it, then gives it back.
	stopAccepting()
	select {
	case <-old.Idle():
		t.Fatal("the drain does not count a job waiting for a sandbox as in flight")
	case <-time.After(100 * time.Millisecond):
	}
	endLifetime()
	old.Wait()
	if state := h.job(id); state.status != "queued" || state.phase != "queued" || state.attempt != 2 || !state.hasTask {
		t.Fatalf("a job cut off while waiting for a sandbox: %+v", state)
	}
	if released := h.sandboxes.released(); len(released) != 1 || released[0].thread != threadID {
		t.Errorf("the cut-off attempt's sandbox place was not given back: %+v", released)
	}

	// The next process claims it, starts it in a fresh sandbox, and it
	// waits again for one, as attempt 2.
	h.runnerWith(h.lifetime, h.lifetime)
	h.until("attempt 2 waits for a sandbox", "deliverable_jobs WHERE phase = 'waiting_for_sandbox' AND attempt_count = 2 AND id = "+strconv.FormatInt(id, 10))
	if released := h.sandboxes.released(); len(released) != 2 || released[1].thread != threadID {
		t.Errorf("attempt 2 did not start from a fresh sandbox: %+v", released)
	}
}
