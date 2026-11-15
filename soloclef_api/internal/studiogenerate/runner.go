package studiogenerate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/inflight"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
)

// The runner's own choices. They are how this code is meant to run, not a
// deployment's settings, and each says why it is what it is.
const (
	// A run says it is alive this often, and a job silent for nine beats
	// is one whose process has gone: long enough that a slow database
	// round trip is not mistaken for a death, short enough that a job a
	// crashed process held is taken up again within a few minutes.
	heartbeatEvery = 20 * time.Second
	abandonedAfter = 3 * time.Minute
	// How often each replica sweeps for jobs a dead process left, and looks
	// for queued jobs it has room for that it was not told about (one
	// queued by another replica that was full, or given back by a process
	// that stopped). A job queued here is claimed at once (Wake); this
	// tick is only for work that arrives from elsewhere, and a minute is
	// short beside the three minutes a dead process takes to be noticed.
	sweepEvery = time.Minute
	// sweepBatch bounds one sweep's statement; what is left is the next
	// minute's.
	sweepBatch = 100
	// The last write of a run is made after its context is gone (a stop,
	// a shutdown), so it has a small time of its own.
	closingWrite = 10 * time.Second
	// A run may pause to ask a question at most this often; the one
	// question a studio kind asks is the infographic's style.
	maxPauses = 3
	// internal_error holds 2000 characters.
	maxInternalError = 1900
)

// The failure codes a job is closed with. The panel shows a generic
// sentence for a code it does not know, so these are for the log and for
// whoever reads the row. A run that made nothing is closed with the code
// its specialist's tools noted instead, when there is one
// (deliverables.Failures, unattended.go).
const (
	codeModel       = "model_unavailable"
	codeTimeout     = "timeout"
	codeStepLimit   = "step_limit"
	codeFailed      = "generation_failed"
	codeNothingMade = "nothing_made"
	codeInterrupted = "interrupted"
	codeCancelled   = "cancelled"
)

// Notifier tells a job's owner that its output is ready or could not be
// made (notify.Notifier).
type Notifier interface {
	StudioFinished(ctx context.Context, run notify.StudioRun) error
}

// reasons is what the owner is told about a failure, by its code. A code
// with no sentence here says only that the output could not be made, which
// is all the person can act on; the detail is for the log.
var reasons = map[string]string{
	codeModel:       "The model could not be reached. Try again in a few minutes.",
	codeTimeout:     "It took longer than this server allows.",
	codeStepLimit:   "It took more steps than one output may.",
	codeNothingMade: "The run ended without making anything. Try again.",
	codeInterrupted: "The server restarted while it was being made. Try again.",
}

// Models answers which model an account's requests run on (chat.Models).
type Models interface {
	For(ctx context.Context, userID string) (model.Model, string, error)
}

// Job is one attempt at one artifact: the row that carries its progress,
// whose it is, which attempt this is, the hidden thread it is made in, and
// the task (read back from the row, stored_task.go).
type Job struct {
	ID     int64
	UserID string
	// Attempt is the row's attempt_count when this process claimed it.
	// Every write of the attempt names it, so that one superseded by a
	// re-queue changes nothing (requeue.go).
	Attempt  int64
	ThreadID int64
	Task     string
	// Style is the infographic style the dialog chose, or "" for a kind
	// that asks nothing.
	Style string
	// Orientation is the infographic's canvas the dialog chose
	// (landscape, portrait, square): the size the image model is asked
	// for. "" for a kind that has none.
	Orientation string
}

// RunnerParts is what a Runner is built from.
type RunnerParts struct {
	Pool   *pgxpool.Pool
	Models Models
	// Specialist is the chat's own (chat.Parts.Deliverables), with its
	// Model unset: each run sets its account's.
	Specialist deliverables.Specialist
	Notifier   Notifier
	// Changes tells the owner's open Studio panels that a job's row moved
	// (claimed, waiting for a sandbox, given back, ended), which is what
	// moves its card without a reload.
	Changes livechange.Publisher
	// Concurrent is how many runs this process makes at once; the rest wait
	// as `queued`, for this process or another.
	Concurrent int
	// Accepting ends when the server is told to stop. No job is claimed
	// after it, by this process: what is queued from then on is the next
	// process's, or another replica's.
	Accepting context.Context
	// Lifetime ends when the server's shutdown drain ends (cmd/api,
	// drain.go). A run still going then is stopped and given back to the
	// queue as its next attempt (requeue.go).
	Lifetime context.Context
	Logger   *slog.Logger
}

// Runner runs the artifact specialist with nobody watching, a bounded
// number at a time, and keeps each job's row true to what is happening.
//
// The row is the queue (queue.go). A job is queued by the route and claimed
// from the database by whichever API process has room, so it is not tied to
// the process that queued it: a deploy that stops this process waits for
// its runs within a bound, and a run cut off at that bound, or by a process
// that died, is queued again as its next attempt and made by the next
// process, starting cleanly (attempt.go). Only a job cut off on every
// attempt it is allowed fails, as interrupted (requeue.go).
type Runner struct {
	pool       *pgxpool.Pool
	models     Models
	notifier   Notifier
	changes    livechange.Publisher
	specialist deliverables.Specialist
	logger     *slog.Logger
	accepting  context.Context
	lifetime   context.Context
	now        func() time.Time
	// beatEvery and sweepEvery are the constants above; only a test names
	// others.
	beatEvery  time.Duration
	sweepEvery time.Duration
	// slots holds one token per run in progress; its capacity is
	// Concurrent. Only the loop (queue.go) adds tokens, so the room it
	// counts is still there when it claims.
	slots chan struct{}
	// nudge wakes the loop: a job was queued here, or a run ended and its
	// place is free. One pending nudge is as good as many.
	nudge chan struct{}
	// runs counts the runs in progress, for the drain (Idle) and for Wait.
	runs inflight.Counter
	// looping is closed when the loop has stopped, for Wait; nil until Run.
	looping chan struct{}
}

// NewRunner builds the runner. Start it with Run.
func NewRunner(parts RunnerParts) (*Runner, error) {
	switch {
	case parts.Pool == nil:
		return nil, errors.New("studiogenerate: the database pool is not set")
	case parts.Models == nil:
		return nil, errors.New("studiogenerate: the models are not set")
	case parts.Notifier == nil:
		return nil, errors.New("studiogenerate: the notifier is not set")
	case parts.Changes == nil:
		return nil, errors.New("studiogenerate: the live-change publisher is not set")
	case parts.Specialist.Store == nil:
		return nil, errors.New("studiogenerate: the specialist has no checkpoint store")
	case parts.Concurrent < 1:
		return nil, fmt.Errorf("studiogenerate: concurrent runs must be at least 1, got %d", parts.Concurrent)
	case parts.Accepting == nil || parts.Lifetime == nil:
		return nil, errors.New("studiogenerate: the accepting and lifetime contexts are not both set")
	case parts.Logger == nil:
		return nil, errors.New("studiogenerate: the logger is not set")
	}
	return &Runner{
		pool: parts.Pool, models: parts.Models, notifier: parts.Notifier, changes: parts.Changes, specialist: parts.Specialist,
		logger: parts.Logger, accepting: parts.Accepting, lifetime: parts.Lifetime,
		now:       func() time.Time { return time.Now().UTC() },
		beatEvery: heartbeatEvery, sweepEvery: sweepEvery,
		slots: make(chan struct{}, parts.Concurrent), nudge: make(chan struct{}, 1),
	}, nil
}

// Wake tells the runner a job was queued. It returns at once; the job is
// claimed by the loop, by this process if it has room.
func (r *Runner) Wake() {
	select {
	case r.nudge <- struct{}{}:
	default:
	}
}

// Idle is closed once no run is in progress in this process: what the
// server's shutdown drain waits for, beside the chat's turns.
func (r *Runner) Idle() <-chan struct{} { return r.runs.Idle() }

// Wait blocks until the loop has stopped and every run has written its
// ending or given its job back. Called at shutdown, after Lifetime has
// ended and before the pool closes.
func (r *Runner) Wait() {
	if r.looping != nil {
		<-r.looping
	}
	r.runs.Wait()
}

// ending is how a run ended, as its row will say.
type ending struct {
	status   DeliverableJobStatus
	code     string
	detail   string
	artifact *int64
}

// The causes a run's context is cancelled with, beside the end of Lifetime.
var (
	// errAsked: the person asked for the output to stop (`cancelling`).
	errAsked = errors.New("studiogenerate: the person stopped the output")
	// errNotOurs: the row is no longer this attempt's (cancelled outright,
	// deleted, or queued again by a sweep that took this process for dead):
	// nothing more is written for it.
	errNotOurs = errors.New("studiogenerate: the job is no longer this attempt's")
)

// run makes one claimed attempt and settles its row. The slot it holds was
// taken by the loop; it is given back here, and the loop is woken to fill it.
func (r *Runner) run(job Job) {
	defer func() {
		<-r.slots
		r.Wake()
		r.runs.End()
	}()
	ctx, stop := context.WithCancelCause(r.lifetime)
	defer stop(nil)
	go r.beat(ctx, job, stop)

	end := r.attempt(ctx, job)
	if ctx.Err() != nil && end.status != DeliverableJobStatusReady {
		// Stopped: that, not whatever the interrupted call returned, is
		// why it ended.
		switch cause := context.Cause(ctx); {
		case errors.Is(cause, errAsked):
			end = ending{status: DeliverableJobStatusCancelled, code: codeCancelled}
		case errors.Is(cause, errNotOurs):
			r.logger.Info("a studio run stopped: its job is no longer this attempt's", slog.Int64("job_id", job.ID), slog.Int64("attempt", job.Attempt))
			return
		default:
			// Lifetime ended: the drain is over and this run did not
			// finish within it.
			r.release(job)
			return
		}
	}
	r.finish(job, end)
}

// changed tells the owner's open clients that a job's row moved. Not on
// its heartbeat: a beat changes nothing a card shows.
func (r *Runner) changed(ctx context.Context, userID string) {
	r.changes.Publish(ctx, userID, livechange.Change{Kind: livechange.Outputs})
}

// notify tells the owner a run is ready or failed; a cancel is not told.
// A notification that cannot be written is logged and the run's ending
// stands.
func (r *Runner) notify(ctx context.Context, job Job, end ending) {
	if end.status != DeliverableJobStatusReady && end.status != DeliverableJobStatusFailed {
		return
	}
	run := notify.StudioRun{UserID: job.UserID, JobID: job.ID, Failed: end.status == DeliverableJobStatusFailed, Reason: reasonFor(end.code)}
	if err := r.notifier.StudioFinished(ctx, run); err != nil {
		r.logger.Error("a studio job's notification could not be written", slog.Int64("job_id", job.ID), slog.String("error", err.Error()))
	}
}

// beat keeps the attempt's heartbeat until ctx ends, and stops the run when
// the row says the person asked for that or that the job is no longer this
// attempt's.
func (r *Runner) beat(ctx context.Context, job Job, stop context.CancelCauseFunc) {
	ticker := time.NewTicker(r.beatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		status, err := New(r.pool).Heartbeat(ctx, HeartbeatParams{Now: r.now(), ID: job.ID, UserID: job.UserID, Attempt: job.Attempt})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			stop(errNotOurs)
			return
		case err == nil && status == string(DeliverableJobStatusCancelling):
			stop(errAsked)
			return
		case err != nil && ctx.Err() == nil:
			// A missed beat is not a reason to stop making the artifact;
			// the next one says so again if the database is still away.
			r.logger.Warn("a studio job's heartbeat failed", slog.Int64("job_id", job.ID), slog.String("error", err.Error()))
		}
	}
}

// make runs the specialist on the task, in the attempt's own conversation,
// and says how it ended.
func (r *Runner) make(ctx context.Context, job Job) ending {
	chatModel, _, err := r.models.For(ctx, job.UserID)
	if err != nil {
		return ending{status: DeliverableJobStatusFailed, code: codeModel, detail: err.Error()}
	}
	specialist := r.specialist
	specialist.Model = chatModel
	// Nobody is watching: wait for a sandbox, note failures, and give the
	// sandbox back at the end (unattended.go).
	r.unattended(&specialist, job)
	defer r.releaseSandbox(ctx, specialist, job)
	delegate, err := specialist.Delegate(deliverables.Owner{UserID: job.UserID, ThreadID: job.ThreadID})
	if err != nil {
		return ending{status: DeliverableJobStatusFailed, code: codeFailed, detail: "building the specialist: " + err.Error()}
	}
	// The specialist's own limit on one stretch of work, as in a chat.
	working, done := context.WithTimeout(ctx, delegate.Timeout)
	defer done()
	// The attempt's conversation is its own and is removed when it ends:
	// the task holds the person's text, and a later attempt starts afresh
	// rather than from a conversation cut off half way (attempt.go).
	conversation := attemptConversation(job.ID, job.Attempt)
	defer func() {
		closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), closingWrite)
		defer cancel()
		if err := specialist.Store.DeleteTree(closing, conversation); err != nil {
			r.logger.Warn("a studio run's conversation was not removed", slog.Int64("job_id", job.ID), slog.String("error", err.Error()))
		}
	}()

	result, err := delegate.Agent.Start(working, conversation, []model.Message{model.User(job.Task)}, event.Discard)
	for pauses := 0; err == nil && result.Outcome == agent.Paused; pauses++ {
		if pauses == maxPauses {
			err = errors.New("the run kept pausing for answers nobody is present to give")
			break
		}
		var decisions []agent.Decision
		if decisions, err = r.answers(working, delegate.Agent, conversation, job); err == nil {
			result, err = delegate.Agent.Resume(working, conversation, decisions, event.Discard)
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded) || working.Err() == context.DeadlineExceeded:
		return ending{status: DeliverableJobStatusFailed, code: codeTimeout, detail: "the run took longer than " + delegate.Timeout.String()}
	case errors.Is(err, agent.ErrStepLimit):
		return ending{status: DeliverableJobStatusFailed, code: codeStepLimit, detail: err.Error()}
	case err != nil:
		return ending{status: DeliverableJobStatusFailed, code: codeFailed, detail: err.Error()}
	}
	// What was made is what was saved under the generation's own thread.
	// The model's sentence about it is not evidence.
	artifact, found, err := r.savedArtifact(ctx, job)
	switch {
	case err != nil:
		return ending{status: DeliverableJobStatusFailed, code: codeFailed, detail: "finding the saved artifact: " + err.Error()}
	case !found:
		return r.nothingMade(job, specialist.Failures, result.Answer)
	}
	return ending{status: DeliverableJobStatusReady, artifact: &artifact}
}

// savedArtifact is the newest artifact saved under the job's hidden thread.
func (r *Runner) savedArtifact(ctx context.Context, job Job) (int64, bool, error) {
	threadID := job.ThreadID
	artifact, err := New(r.pool).ArtifactOfThread(ctx, ArtifactOfThreadParams{UserID: job.UserID, ThreadID: &threadID})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return artifact, err == nil, err
}

// answers decides every waiting call of a paused run. The one question a
// studio kind asks is the infographic's style, which the dialog asked
// first; it is answered with that choice, and with the dialog's
// orientation beside it, which the question itself does not ask. Any other question is closed and
// any gate refused: nobody is present, and a run must not approve for them.
func (r *Runner) answers(ctx context.Context, specialist *agent.Agent, thread string, job Job) ([]agent.Decision, error) {
	waiting, err := specialist.Pending(ctx, thread)
	if err != nil {
		return nil, err
	}
	decisions := make([]agent.Decision, 0, len(waiting))
	for _, call := range waiting {
		switch {
		case call.Question == nil:
			decisions = append(decisions, agent.Decision{CallID: call.CallID, Verdict: agent.Reject, Reason: "nobody is present to approve this"})
		case job.Style != "":
			answer, err := deliverables.StyleAnswer(job.Style, job.Orientation)
			if err != nil {
				return nil, err
			}
			decisions = append(decisions, agent.Decision{CallID: call.CallID, Verdict: agent.Answer, Answer: answer})
		default:
			decisions = append(decisions, agent.Decision{CallID: call.CallID, Verdict: agent.Answer, Answer: json.RawMessage(`{"type":"cancel"}`)})
		}
	}
	return decisions, nil
}

// finish writes the attempt's ending over that attempt still in progress.
// It has a context of its own: the run's is usually gone by now.
func (r *Runner) finish(job Job, end ending) {
	closing, cancel := context.WithTimeout(context.Background(), closingWrite)
	defer cancel()
	arg := FinishJobParams{Status: end.status, Now: r.now(), ID: job.ID, UserID: job.UserID, Attempt: job.Attempt, ArtifactID: end.artifact}
	phase := string(end.status)
	arg.Phase = &phase
	if end.status == DeliverableJobStatusReady {
		arg.Progress = 100
	}
	if end.code != "" {
		arg.FailureCode = &end.code
	}
	if end.detail != "" {
		detail := clip(end.detail, maxInternalError)
		arg.InternalError = &detail
	}
	written, err := New(r.pool).FinishJob(closing, arg)
	if err != nil {
		// The sweep takes it up again in a few minutes; say why it will
		// look late.
		r.logger.Error("a studio job's ending could not be written", slog.Int64("job_id", job.ID),
			slog.String("status", string(end.status)), slog.String("error", err.Error()))
		return
	}
	// Only an ending this attempt wrote is reported: a job cancelled
	// outright meanwhile keeps its cancellation, and nobody needs telling.
	if written == 1 {
		r.changed(closing, job.UserID)
		r.notify(closing, job, end)
	}
	level := slog.LevelInfo
	if end.status == DeliverableJobStatusFailed {
		level = slog.LevelWarn
	}
	r.logger.Log(closing, level, "a studio job ended", slog.Int64("job_id", job.ID), slog.Int64("attempt", job.Attempt),
		slog.String("status", string(end.status)), slog.String("failure_code", end.code), slog.String("detail", clip(end.detail, 300)))
}
