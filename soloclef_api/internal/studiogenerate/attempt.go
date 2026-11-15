package studiogenerate

import (
	"context"
	"log/slog"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// attempt makes one attempt at a job, after making sure it starts cleanly.
//
// A first attempt has nothing before it: its hidden thread is new, so are
// its sandbox and its conversation. A later one follows an attempt that was
// cut off at some unknown point, and three things that attempt may have
// left are dealt with before anything is made:
//
//   - The artifact it may already have saved. A save is one transaction (the
//     document, the artifact, its files and their rows, or none of them:
//     deliverables.Database), and only a file that passed verification is
//     saved, so what is there is whole. It is the job's result, and making
//     the artifact again would put a second copy of it in the person's
//     library. The attempt ends ready with it.
//   - Its conversations. Each attempt has its own (attemptConversation), so
//     a new one never continues a conversation cut off half way, and the
//     earlier ones, which a process that died could not remove, are removed
//     here: they hold the task, which is the person's text.
//   - Its sandbox, which is the thread's and outlives a process: every run
//     gives its sandbox back as it ends (unattended.go), but a process that
//     died could not, and its sandbox may hold files written half way. It is
//     removed, so this attempt builds in a fresh one, which it too gives back
//     at its end. A half-written file could not be saved anyway (a save
//     needs this process's own verification of it), but nothing should be
//     built on one.
func (r *Runner) attempt(ctx context.Context, job Job) ending {
	if job.Attempt > 1 {
		artifact, found, err := r.savedArtifact(ctx, job)
		if err != nil {
			return ending{status: DeliverableJobStatusFailed, code: codeFailed, detail: "looking for an earlier attempt's artifact: " + err.Error()}
		}
		if found {
			r.logger.Info("a studio job cut off after its artifact was saved is finished with that artifact",
				slog.Int64("job_id", job.ID), slog.Int64("attempt", job.Attempt), slog.Int64("artifact_id", artifact))
			return ending{status: DeliverableJobStatusReady, artifact: &artifact}
		}
		r.clearEarlierAttempts(ctx, job)
	}
	return r.make(ctx, job)
}

// clearEarlierAttempts removes what the attempts before this one left. What
// cannot be removed is logged and the attempt goes on: a conversation left
// behind is never read again (this attempt has its own), and a sandbox that
// cannot be reached now will fail this attempt's first build with its own
// reason.
func (r *Runner) clearEarlierAttempts(ctx context.Context, job Job) {
	for earlier := int64(1); earlier < job.Attempt; earlier++ {
		if err := r.specialist.Store.DeleteTree(ctx, attemptConversation(job.ID, earlier)); err != nil {
			r.logger.Warn("an earlier attempt's conversation was not removed", slog.Int64("job_id", job.ID),
				slog.Int64("attempt", earlier), slog.String("error", err.Error()))
		}
	}
	// The specialist's own release, the one every run gives its sandbox
	// back with at its end (unattended.go): one way a sandbox goes.
	if err := r.specialist.ReleaseSandbox(ctx, deliverables.Owner{UserID: job.UserID, ThreadID: job.ThreadID}); err != nil {
		r.logger.Warn("an earlier attempt's sandbox was not removed", slog.Int64("job_id", job.ID), slog.String("error", err.Error()))
	}
}

// attemptConversation names one attempt's conversation in the checkpoint
// store. It is per attempt, not per job, so that a process that lost its job
// to a re-queue, and has not yet noticed, never writes into the
// conversation of the attempt that replaced it.
func attemptConversation(jobID, attempt int64) string {
	return "studio-job-" + strconv.FormatInt(jobID, 10) + "-attempt-" + strconv.FormatInt(attempt, 10)
}
