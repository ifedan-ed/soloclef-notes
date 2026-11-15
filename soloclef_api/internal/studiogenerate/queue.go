package studiogenerate

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// Run starts the runner's loop and returns at once. The loop claims queued
// jobs while this process is accepting work, and sweeps for jobs a dead
// process left (requeue.go): once at start, which is what takes up a job a
// deploy cut off, and every sweepEvery after.
//
// # When a job is claimed
//
// Never by polling faster than the sweep. The loop claims when it is woken:
// by the route that queued a job here (Wake), by a run that ended and freed
// a place, and by the minute's tick, which is what finds a job queued
// elsewhere (another replica that was full, a process that gave its job back
// as it stopped). Each claim asks for no more jobs than this process has
// places free, so a claimed job never waits in memory: it is running, and
// beating, from the moment it leaves the queue.
//
// # Why two replicas never take the same job
//
// The claim is one statement (ClaimQueuedJobs): it locks the queued rows
// it chose with FOR UPDATE SKIP LOCKED and moves them to running. A row another
// replica's claim holds is skipped, not waited for, and one it has moved is
// no longer queued, so each queued row is taken by exactly one claim. From
// then on every write names the attempt it was claimed as; a job that is
// taken from a process (re-queued by a sweep that found it silent) has a
// new attempt number, and the old process's writes match nothing.
func (r *Runner) Run() {
	r.looping = make(chan struct{})
	go func() {
		defer close(r.looping)
		r.loop()
	}()
}

func (r *Runner) loop() {
	ticker := time.NewTicker(r.sweepEvery)
	defer ticker.Stop()
	r.sweep()
	for {
		r.claim()
		select {
		case <-r.accepting.Done():
			// Stopping: no more claims, and the sweep is left to the
			// processes that go on (or the next one, which sweeps as it
			// starts). The runs already claimed carry on until Lifetime.
			return
		case <-r.lifetime.Done():
			// Lifetime ended without a stop signal: the server is leaving
			// by an error (cmd/api returns early), and Wait must not wait
			// for a loop that only the signal would end.
			return
		case <-r.nudge:
		case <-ticker.C:
			r.sweep()
		}
	}
}

// claim takes as many queued jobs as this process has places for, and
// starts each.
func (r *Runner) claim() {
	free := cap(r.slots) - len(r.slots)
	if free == 0 || r.accepting.Err() != nil || r.lifetime.Err() != nil {
		return
	}
	claimed, err := claimFairly(r.accepting, r.pool, r.now(), runsPerAccount, free)
	if err != nil {
		if r.accepting.Err() == nil {
			// The next nudge or tick claims again; the jobs stay queued.
			r.logger.Error("queued studio jobs could not be claimed", slog.String("error", err.Error()))
		}
		return
	}
	for _, row := range claimed {
		job := Job{ID: row.ID, UserID: row.UserID, Attempt: row.AttemptCount}
		// The place and the count are taken here, before the goroutine,
		// so that the next claim sees the place as taken and the drain
		// sees the run as in progress.
		r.slots <- struct{}{}
		r.runs.Begin()
		if failure := readClaimed(row, &job); failure != nil {
			go func() {
				defer func() { <-r.slots; r.Wake(); r.runs.End() }()
				r.finish(job, *failure)
			}()
			continue
		}
		go r.run(job)
	}
}

// claimFairly takes up to most queued jobs, each account's share at most
// (ClaimQueuedJobs, fair_share.go), with every other replica's claim held
// off until this one commits (LockStudioClaims), so the share counts the
// runs every replica has started. The runner claims through it with
// runsPerAccount; a test may name another share.
func claimFairly(ctx context.Context, pool *pgxpool.Pool, now time.Time, perAccount int64, most int) ([]ClaimQueuedJobsRow, error) {
	return database.Write(ctx, pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) ([]ClaimQueuedJobsRow, error) {
		q := New(tx)
		if err := q.LockStudioClaims(ctx); err != nil {
			return nil, err
		}
		return q.ClaimQueuedJobs(ctx, ClaimQueuedJobsParams{Now: now, PerAccount: perAccount, Most: int32(most)})
	})
}

// readClaimed fills the job from its row, or says why it cannot be made. A
// job whose hidden thread was deleted has nowhere to be made: the sandbox
// and the artifact's attribution are keyed by that thread.
func readClaimed(row ClaimQueuedJobsRow, job *Job) *ending {
	if row.ThreadID == nil {
		return &ending{status: DeliverableJobStatusFailed, code: codeFailed, detail: "the generation's hidden thread was deleted"}
	}
	job.ThreadID = *row.ThreadID
	if err := decodeTask(row.Request, job); err != nil {
		return &ending{status: DeliverableJobStatusFailed, code: codeFailed, detail: err.Error()}
	}
	return nil
}
