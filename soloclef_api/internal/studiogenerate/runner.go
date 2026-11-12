package studiogenerate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// The runner's own choices. They are how this code is meant to run, not a
// deployment's settings, and each says why it is what it is.
const (
	// A run says it is alive this often, and a job silent for nine beats
	// is one whose process has gone: long enough that a slow database
	// round trip is not mistaken for a death, short enough that the panel
	// stops spinning within a few minutes of a crash.
	heartbeatEvery = 20 * time.Second
	abandonedAfter = 3 * time.Minute
	sweepEvery     = time.Minute
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
// whoever reads the row.
const (
	codeModel       = "model_unavailable"
	codeTimeout     = "timeout"
	codeStepLimit   = "step_limit"
	codeFailed      = "generation_failed"
	codeNothingMade = "nothing_made"
	codeInterrupted = "interrupted"
	codeCancelled   = "cancelled"
)

// Models answers which model an account's requests run on (chat.Models).
type Models interface {
	For(ctx context.Context, userID string) (model.Model, string, error)
}

// Job is one artifact to make: the row that carries its progress, whose it
// is, the hidden thread it is made in, and the task.
type Job struct {
	ID       int64
	UserID   string
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

// Runner runs the artifact specialist with nobody watching, a bounded
// number at a time, and keeps each job's row true to what is happening.
//
// A run lives in this process, like a chat turn: it outlives the request
// that started it and is told to stop when the server shuts down. What
// makes that safe is the row. It is claimed only from `queued`, it beats
// while it runs, every ending is written only over a run still in progress,
// and Sweep fails the jobs of a process that died.
type Runner struct {
	pool       *pgxpool.Pool
	models     Models
	specialist deliverables.Specialist
	logger     *slog.Logger
	lifetime   context.Context
	now        func() time.Time
	// beatEvery is heartbeatEvery; only a test names another.
	beatEvery time.Duration
	slots     chan struct{}
	running   sync.WaitGroup
}

// NewRunner builds the runner. specialist is the chat's own
// (chat.Parts.Deliverables), with its Model unset: each run sets its
// account's. concurrent is how many runs this process makes at once; the
// rest wait as `queued`. lifetime is cancelled when the server shuts down.
func NewRunner(pool *pgxpool.Pool, models Models, specialist deliverables.Specialist, concurrent int, lifetime context.Context, logger *slog.Logger) (*Runner, error) {
	switch {
	case pool == nil:
		return nil, errors.New("studiogenerate: the database pool is not set")
	case models == nil:
		return nil, errors.New("studiogenerate: the models are not set")
	case specialist.Store == nil:
		return nil, errors.New("studiogenerate: the specialist has no checkpoint store")
	case concurrent < 1:
		return nil, fmt.Errorf("studiogenerate: concurrent runs must be at least 1, got %d", concurrent)
	case lifetime == nil:
		return nil, errors.New("studiogenerate: the lifetime context is not set")
	case logger == nil:
		return nil, errors.New("studiogenerate: the logger is not set")
	}
	return &Runner{
		pool: pool, models: models, specialist: specialist, logger: logger, lifetime: lifetime,
		now: func() time.Time { return time.Now().UTC() }, beatEvery: heartbeatEvery, slots: make(chan struct{}, concurrent),
	}, nil
}

// Start runs the job in the background and returns at once.
func (r *Runner) Start(job Job) {
	r.running.Add(1)
	go func() {
		defer r.running.Done()
		r.run(job)
	}()
}

// Wait blocks until every run has written its ending. Called at shutdown,
// after the lifetime context is cancelled and before the pool closes.
func (r *Runner) Wait() { r.running.Wait() }

// Sweep fails the artifact jobs of processes that died, now and then every
// minute until the lifetime ends. It runs in every replica; the statement
// is idempotent, so two sweeping at once agree.
func (r *Runner) Sweep() {
	r.running.Add(1)
	go func() {
		defer r.running.Done()
		ticker := time.NewTicker(sweepEvery)
		defer ticker.Stop()
		for {
			now := r.now()
			failed, err := New(r.pool).FailAbandonedJobs(r.lifetime, FailAbandonedJobsParams{Now: now, SilentSince: now.Add(-abandonedAfter)})
			switch {
			case err != nil && r.lifetime.Err() == nil:
				r.logger.Error("the sweep of abandoned studio jobs failed", slog.String("error", err.Error()))
			case failed > 0:
				r.logger.Warn("studio jobs whose process went away were failed", slog.Int64("jobs", failed))
			}
			select {
			case <-r.lifetime.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// ending is how a run ended, as its row will say.
type ending struct {
	status   DeliverableJobStatus
	code     string
	detail   string
	artifact *int64
}

func (r *Runner) run(job Job) {
	ctx, stop := context.WithCancel(r.lifetime)
	defer stop()
	// asked is set when the person stopped the output: the heartbeat read
	// `cancelling`, or found the job already closed.
	var asked atomic.Bool
	go r.beat(ctx, job, &asked, stop)

	// A place among the runs this process makes at once. The job waits as
	// `queued`, beating, and can be cancelled while it waits.
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		r.finish(job, r.stopped(&asked))
		return
	}
	claimed, err := New(r.pool).ClaimJob(ctx, ClaimJobParams{Now: r.now(), ID: job.ID, UserID: job.UserID})
	if err != nil {
		r.finish(job, ending{status: DeliverableJobStatusFailed, code: codeFailed, detail: "claiming the job: " + err.Error()})
		return
	}
	if claimed == 0 {
		// Cancelled while it waited: the row already says so.
		return
	}
	end := r.make(ctx, job)
	if ctx.Err() != nil && end.status != DeliverableJobStatusReady {
		// Stopped, by the person or by a shutdown: that, not whatever the
		// interrupted call returned, is why it ended.
		end = r.stopped(&asked)
	}
	r.finish(job, end)
}

// stopped is the ending of a run whose context was cancelled.
func (r *Runner) stopped(asked *atomic.Bool) ending {
	if asked.Load() {
		return ending{status: DeliverableJobStatusCancelled, code: codeCancelled}
	}
	return ending{status: DeliverableJobStatusFailed, code: codeInterrupted, detail: "the server shut down while this was being made"}
}

// beat keeps the job's heartbeat until ctx ends, and stops the run when the
// row says the person asked for that.
func (r *Runner) beat(ctx context.Context, job Job, asked *atomic.Bool, stop context.CancelFunc) {
	ticker := time.NewTicker(r.beatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		status, err := New(r.pool).Heartbeat(ctx, HeartbeatParams{Now: r.now(), ID: job.ID, UserID: job.UserID})
		switch {
		case errors.Is(err, pgx.ErrNoRows), err == nil && status == string(DeliverableJobStatusCancelling):
			// No row in progress means the job was cancelled outright or
			// deleted; `cancelling` means it was asked to stop.
			asked.Store(true)
			stop()
			return
		case err != nil && ctx.Err() == nil:
			// A missed beat is not a reason to stop making the artifact;
			// the next one says so again if the database is still away.
			r.logger.Warn("a studio job's heartbeat failed", slog.Int64("job_id", job.ID), slog.String("error", err.Error()))
		}
	}
}

// make runs the specialist on the task and says how it ended.
func (r *Runner) make(ctx context.Context, job Job) ending {
	chatModel, _, err := r.models.For(ctx, job.UserID)
	if err != nil {
		return ending{status: DeliverableJobStatusFailed, code: codeModel, detail: err.Error()}
	}
	specialist := r.specialist
	specialist.Model = chatModel
	delegate, err := specialist.Delegate(deliverables.Owner{UserID: job.UserID, ThreadID: job.ThreadID})
	if err != nil {
		return ending{status: DeliverableJobStatusFailed, code: codeFailed, detail: "building the specialist: " + err.Error()}
	}
	// The specialist's own limit on one stretch of work, as in a chat.
	working, done := context.WithTimeout(ctx, delegate.Timeout)
	defer done()
	// The run's conversation is its own and is removed when it ends:
	// nothing resumes a studio run, and the task holds the person's text.
	thread := "studio-job-" + strconv.FormatInt(job.ID, 10)
	defer func() {
		closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), closingWrite)
		defer cancel()
		if err := specialist.Store.DeleteTree(closing, thread); err != nil {
			r.logger.Warn("a studio run's conversation was not removed", slog.Int64("job_id", job.ID), slog.String("error", err.Error()))
		}
	}()

	result, err := delegate.Agent.Start(working, thread, []model.Message{model.User(job.Task)}, event.Discard)
	for pauses := 0; err == nil && result.Outcome == agent.Paused; pauses++ {
		if pauses == maxPauses {
			err = errors.New("the run kept pausing for answers nobody is present to give")
			break
		}
		var decisions []agent.Decision
		if decisions, err = r.answers(working, delegate.Agent, thread, job); err == nil {
			result, err = delegate.Agent.Resume(working, thread, decisions, event.Discard)
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
	threadID := job.ThreadID
	artifact, err := New(r.pool).ArtifactOfThread(ctx, ArtifactOfThreadParams{UserID: job.UserID, ThreadID: &threadID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ending{status: DeliverableJobStatusFailed, code: codeNothingMade, detail: result.Answer}
	case err != nil:
		return ending{status: DeliverableJobStatusFailed, code: codeFailed, detail: "finding the saved artifact: " + err.Error()}
	}
	return ending{status: DeliverableJobStatusReady, artifact: &artifact}
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

// finish writes the run's ending over a run still in progress. It has a
// context of its own: the run's is usually gone by now.
func (r *Runner) finish(job Job, end ending) {
	closing, cancel := context.WithTimeout(context.Background(), closingWrite)
	defer cancel()
	arg := FinishJobParams{Status: end.status, Now: r.now(), ID: job.ID, UserID: job.UserID, ArtifactID: end.artifact}
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
	if _, err := New(r.pool).FinishJob(closing, arg); err != nil {
		// The sweep fails it in a few minutes; say why it will look late.
		r.logger.Error("a studio job's ending could not be written", slog.Int64("job_id", job.ID),
			slog.String("status", string(end.status)), slog.String("error", err.Error()))
		return
	}
	level := slog.LevelInfo
	if end.status == DeliverableJobStatusFailed {
		level = slog.LevelWarn
	}
	r.logger.Log(closing, level, "a studio job ended", slog.Int64("job_id", job.ID), slog.String("status", string(end.status)),
		slog.String("failure_code", end.code), slog.String("detail", clip(end.detail, 300)))
}
