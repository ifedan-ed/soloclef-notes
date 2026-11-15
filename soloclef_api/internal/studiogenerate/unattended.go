package studiogenerate

import (
	"context"
	"log/slog"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// What a run nobody is watching needs of the specialist that a chat turn
// does not: to wait for a sandbox when the account has none free, and to
// say so on its row; to give its sandbox back the moment it ends; and to
// be closed, when it made nothing, with this server's own reason rather
// than with what the specialist model wrote about itself. Kept here, apart
// from runner.go, so the runner's life (claim, beat, finish) reads alone.

// The phases a running job shows (deliverable_jobs.phase). "generating" is
// what ClaimQueuedJobs writes; the panel shows "waiting_for_sandbox" by
// name. A job waiting for a sandbox is a run in progress like any other: it
// beats, so no sweep takes it for dead; a deploy's drain waits for it; and
// one still waiting when the drain ends is given back to the queue, its
// phase back to "queued" (requeue.go), and waits again, in a fresh sandbox
// request, in the process that claims it next.
const (
	phaseGenerating        = "generating"
	phaseWaitingForSandbox = "waiting_for_sandbox"
)

// unattended gives a run's own copy of the specialist what a run nobody
// watches needs: a bounded wait for a sandbox, reported on the job's row,
// and a collector of the failures its tools meet.
func (r *Runner) unattended(specialist *deliverables.Specialist, job Job) {
	specialist.SandboxWait = deliverables.SandboxWait{For: deliverables.UnattendedSandboxWait, Waiting: r.showWaiting(job)}
	specialist.Failures = &deliverables.Failures{}
}

// showWaiting writes the job's phase as its wait for a sandbox starts and
// ends. One short statement each way, on a pooled connection that goes
// straight back: the wait itself holds no connection. A write that fails
// is logged and the run goes on; the phase is a courtesy, the heartbeat is
// what keeps the job alive.
func (r *Runner) showWaiting(job Job) func(ctx context.Context, waiting bool) {
	return func(ctx context.Context, waiting bool) {
		phase := phaseGenerating
		if waiting {
			phase = phaseWaitingForSandbox
		}
		closing, cancel := context.WithTimeout(ctx, closingWrite)
		defer cancel()
		if err := New(r.pool).SetRunningPhase(closing, SetRunningPhaseParams{Phase: &phase, Now: r.now(), ID: job.ID, UserID: job.UserID, Attempt: job.Attempt}); err != nil {
			r.logger.Warn("a studio job's phase could not be written", slog.Int64("job_id", job.ID),
				slog.String("phase", phase), slog.String("error", err.Error()))
		}
	}
}

// releaseSandbox gives back the run's sandbox when it ends, whatever the
// ending: the run's thread is its own and nothing resumes it, so keeping
// the sandbox to its idle time to live would hold a place in the account's
// limit that another output is waiting for. If this fails, or the process
// dies first, the sandbox still expires at that time to live.
func (r *Runner) releaseSandbox(ctx context.Context, specialist deliverables.Specialist, job Job) {
	closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), closingWrite)
	defer cancel()
	if err := specialist.ReleaseSandbox(closing, deliverables.Owner{UserID: job.UserID, ThreadID: job.ThreadID}); err != nil {
		r.logger.Warn("a studio run's sandbox was not released; it expires when idle", slog.Int64("job_id", job.ID),
			slog.String("error", err.Error()))
	}
}

// nothingMade is the ending of a run that saved nothing. Its code is the
// last failure the specialist's tools noted, which has a sentence of this
// server's (deliverables.FailureSentence), or nothing_made when the run
// met none (the model gave up, or never called the tool that saves). The
// specialist model's own answer is logged and goes nowhere else: it is the
// model's account of itself, which can claim a save that did not happen.
func (r *Runner) nothingMade(job Job, failures *deliverables.Failures, answer string) ending {
	r.logger.Info("a studio run ended without saving anything; the specialist model said", slog.Int64("job_id", job.ID),
		slog.String("model_answer", clip(answer, maxInternalError)))
	if failure, noted := failures.Last(); noted {
		return ending{status: DeliverableJobStatusFailed, code: failure.Code, detail: failure.Cause}
	}
	return ending{status: DeliverableJobStatusFailed, code: codeNothingMade, detail: "the specialist ended without saving an artifact"}
}

// reasonFor is the sentence the owner is told for a failure code: the
// runner's own codes first, then the specialist's. A code with neither
// has none, and the notification says only that the output could not be
// made.
func reasonFor(code string) string {
	if reason, known := reasons[code]; known {
		return reason
	}
	reason, _ := deliverables.FailureSentence(code)
	return reason
}
