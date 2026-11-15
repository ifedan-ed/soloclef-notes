package studiogenerate

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

// maxAttempts is how many times one artifact job is made before a cut-off
// fails it: the first attempt and two more. A job is cut off only by a
// deploy that outlasted its drain (cmd/api, drain.go) or by a process that
// died, and either happening to one job twice more says something about the
// job itself -- a run that takes the process down with it, out of memory on
// every try -- which must end, as interrupted, rather than loop. It is the
// worker's rule for a video too (two takeovers, soloclef_worker/cmd/worker).
const maxAttempts = 3

// The internal reasons a job cut off for the last time is failed with; the
// person is told reasons[codeInterrupted].
const (
	shutdownReason  = "the server shut down while this was being made, on every attempt it is allowed"
	abandonedReason = "the process making this stopped answering, on every attempt it is allowed (or the job holds no task to make again)"
)

// release gives back an attempt that the end of the drain cut off: the job
// is queued again as its next attempt, which the next process takes up as
// it starts, or, with no attempt left, failed and its owner told. A stop the
// person had asked for is finished as the cancel it is.
//
// It has a context of its own, as finish does: the run's has ended, and the
// process is about to.
func (r *Runner) release(job Job) {
	closing, cancel := context.WithTimeout(context.Background(), closingWrite)
	defer cancel()
	released, err := New(r.pool).ReleaseJob(closing, ReleaseJobParams{
		Reason: shutdownReason, Now: r.now(), MaxAttempts: maxAttempts,
		ID: job.ID, UserID: job.UserID, Attempt: job.Attempt,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No longer this attempt's: cancelled outright or deleted while it
		// was being stopped.
		return
	case err != nil:
		// The row still says running and stops beating now; the sweep of
		// the next process takes it up once it is silent (abandonedAfter).
		r.logger.Error("a studio job cut off by the shutdown could not be given back; it is taken up again once its heartbeat is stale",
			slog.Int64("job_id", job.ID), slog.String("error", err.Error()))
		return
	}
	r.logger.Warn("a studio job was cut off by the shutdown", slog.Int64("job_id", job.ID),
		slog.Int64("attempt", job.Attempt), slog.String("now", released.Status), slog.Int64("next_attempt", released.AttemptCount))
	if released.Status == string(DeliverableJobStatusFailed) {
		r.notify(closing, job, ending{status: DeliverableJobStatusFailed, code: codeInterrupted})
	}
}

// sweep takes up the artifact jobs of processes that went away: each is
// queued again as its next attempt, for this process or another to claim,
// or failed once its attempts are spent. Every replica sweeps; the
// statement shares its rows between sweeps that overlap (SKIP LOCKED) and
// advances each job's attempt once.
func (r *Runner) sweep() {
	now := r.now()
	swept, err := New(r.pool).RequeueAbandonedJobs(r.accepting, RequeueAbandonedJobsParams{
		Reason: abandonedReason, Now: now, MaxAttempts: maxAttempts,
		SilentSince: now.Add(-abandonedAfter), Most: sweepBatch,
	})
	if err != nil {
		if r.accepting.Err() == nil {
			r.logger.Error("the sweep of abandoned studio jobs failed", slog.String("error", err.Error()))
		}
		return
	}
	for _, job := range swept {
		r.logger.Warn("a studio job whose process went away was taken up", slog.Int64("job_id", job.ID),
			slog.String("now", job.Status), slog.Int64("attempt", job.AttemptCount))
		// A cancel the dead process never finished is the person's own
		// doing; a job queued again is still being made. Only a failure is
		// reported.
		if job.Status == string(DeliverableJobStatusFailed) {
			r.notify(r.accepting, Job{ID: job.ID, UserID: job.UserID}, ending{status: DeliverableJobStatusFailed, code: codeInterrupted})
		}
	}
	r.forgetEnded()
}

// forgetEnded removes the conversations of attempts that are no longer
// running (ForgetEndedConversations says which and why). It runs after the
// requeue above, so a job that sweep has just failed on its last attempt
// loses its conversations in the same minute. What is not removed now is
// removed by the next sweep: a conversation nobody reads is harm only for
// as long as it is kept, so a failure is logged and not retried here.
func (r *Runner) forgetEnded() {
	if _, err := New(r.pool).ForgetEndedConversations(r.accepting, sweepBatch); err != nil && r.accepting.Err() == nil {
		r.logger.Error("the conversations of ended studio jobs could not be removed", slog.String("error", err.Error()))
	}
}
