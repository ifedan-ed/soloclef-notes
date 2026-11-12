// Package videojob makes a video: narrated slides, rendered in the sandbox.
//
// # What a video is made of
//
// The account's chat model writes the scenes (each a narration, its
// on-screen text, and a Remotion TSX module that draws it); the narration
// is voiced through the proxy's speech route and measured; the scenes are
// built and the last frame of each is rendered and its layout measured
// (layout.go); the video is rendered (every frame, or each scene's entrance with
// its last still held to the narration); the MP4 is checked; and it is kept
// as an artifact with its slides and their narration. A build that fails,
// a finding of the layout check, or a failed check sends the scenes back
// to the model for a repair, twice at most.
//
// It is the Python worker's pipeline (app/deliverables/video/executor.py,
// the deliverable job task and the sandbox tools it called), on the
// user-keyed schema and with its defects fixed; each fix is said where it
// is made.
//
// # The message
//
// `deliverables.execute_queued`, keyword `job_id`, on the heavy queue,
// published by the API tier's chat under the attempt's task id
// (soloclef_assistant/deliverables/video.go). The claim takes the
// job only for the message whose id is the row's celery_task_id, so a
// duplicate delivery or an old attempt's message never runs it twice.
//
// # Cancel, retry, restart
//
//   - A cancel moves the row to cancelling. The watch (watch.go) sees it
//     within a status interval and stops the work; the sandbox is removed;
//     the row becomes cancelled. The save re-reads the row under a lock held
//     to its commit (rows.go, Deliver), so a cancel that lands during the
//     render is never written over by a finished video.
//   - A retry moves a failed or cancelled row to queued under the next
//     attempt's task id, with a sandbox of its own; an old attempt still
//     running finds its writes refused and stops.
//   - A worker that is stopping gives its attempt back to the queue
//     (Release) and the message goes back with it. A worker that died
//     leaves a running row whose heartbeat goes stale; when its message is
//     delivered again, the claim takes the attempt over (twice at most).
//     The Python tier needed its minute-by-minute reconciler for both, and
//     the Go stack schedules nothing.
package videojob

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

// Task is the message's name.
const Task = "deliverables.execute_queued"

// Models gives the job the model the account's long written work is
// drafted by (llm.Writers).
type Models interface {
	StudioModel(ctx context.Context, userID string) (string, error)
	Client(ctx context.Context, userID, name string) (model.Model, error)
}

// Indexer asks for an artifact's document to be made searchable.
type Indexer interface {
	Index(ctx context.Context, documentID int64) error
}

// Timing is how the job paces itself: decisions about the job, made in
// cmd/worker with their reasons.
type Timing struct {
	// CallTimeout bounds one model call.
	CallTimeout time.Duration
	// StatusInterval is how often the watch asks whether the job was
	// cancelled.
	StatusInterval time.Duration
	// KeepAlive is how often the watch says the worker lives.
	KeepAlive time.Duration
	// Claim decides when a dead worker's attempt is taken over.
	Claim ClaimRules
	// RepairCycles is how many times the scenes may be repaired.
	RepairCycles int
	// NarrationConcurrency is how many scenes are voiced at once.
	NarrationConcurrency int
}

// Job runs deliverables.execute_queued messages.
type Job struct {
	Rows      Rows
	Sandboxes Sandboxes
	Models    Models
	Voicer    Voicer
	Audio     Measurer
	Files     filestore.Writer
	Indexer   Indexer
	Config    Config
	Timing    Timing
	Log       *slog.Logger
	// Measured, when set, is told each delivered video's stages. The log
	// line says the same; this is for a test to read.
	Measured func(jobID int64, stages []Stage)

	once    sync.Once
	renders chan struct{}
}

// Check reports what is missing. Called once at start-up.
func (j *Job) Check() error {
	switch {
	case j.Rows == nil || j.Sandboxes == nil || j.Models == nil || j.Voicer == nil || j.Audio == nil ||
		j.Files == nil || j.Indexer == nil || j.Log == nil:
		return errors.New("videojob: a dependency is missing")
	case !j.Config.Enabled:
		return errors.New("videojob: the render is not enabled; register Refusal instead")
	case j.Config.Mode != ModeFrames && j.Config.Mode != ModeStills:
		return errors.New("videojob: no render mode")
	case j.Config.MaxRenders < 1 || j.Config.RenderTimeout <= 0 || j.Config.Sandbox.CommandTimeout <= 0:
		return errors.New("videojob: the render limits are not set")
	case j.Timing.CallTimeout <= 0 || j.Timing.StatusInterval <= 0 || j.Timing.KeepAlive <= 0 ||
		j.Timing.Claim.StaleAfter < 3*j.Timing.KeepAlive || j.Timing.Claim.MaxTakeovers < 0 ||
		j.Timing.RepairCycles < 0 || j.Timing.NarrationConcurrency < 1:
		return errors.New("videojob: the timing is incomplete, or a takeover would not wait for three missed keep-alives")
	}
	return nil
}

// The two ways an attempt stops that are not failures.
var (
	// errCancelled: the person cancelled the video.
	errCancelled = errors.New("videojob: the video was cancelled")
	// errSuperseded: the attempt is no longer this worker's (retried,
	// taken over, deleted).
	errSuperseded = errors.New("videojob: the attempt moved on while it was being made")
)

// Run is the jobs.Handler.
func (j *Job) Run(ctx context.Context, msg *celery.Message) error {
	jobID, err := jobOf(msg)
	if err != nil {
		return err
	}
	claim, err := j.Rows.Claim(ctx, jobID, msg.ID, j.Timing.Claim)
	if err != nil {
		// Nothing was claimed, so nothing is to be marked; the message
		// goes back and is tried again.
		return fmt.Errorf("%w: claiming video job %d: %w", broker.ErrRequeue, jobID, err)
	}
	job := claim.Job
	log := j.Log.With("job", jobID, "attempt", job.Attempt)
	switch claim.Outcome {
	case claimNothing:
		log.Info("nothing to make for this message", "why", claim.Why)
		return nil
	case claimFinishCancel:
		j.terminate(ctx, log, job)
		log.Info("a cancel whose worker had stopped is finished")
		return j.Rows.Cancelled(context.WithoutCancel(ctx), job)
	}
	if job.Takeovers > 0 {
		// The dead worker's sandbox may still be rendering; it goes before
		// a new one is made under the same name.
		log.Warn("taking over an attempt whose worker stopped answering", "takeover", job.Takeovers)
		j.terminate(ctx, log, job)
	}

	work, stop := context.WithCancelCause(ctx)
	watching := make(chan struct{})
	go func() {
		defer close(watching)
		j.watch(work, log, job, stop)
	}()
	measured := newStages()
	err = j.make(work, log, job, measured)
	stop(nil)
	<-watching
	return j.conclude(ctx, work, log, job, measured, err)
}

// conclude settles the row after an attempt, and always removes the
// attempt's sandbox.
func (j *Job) conclude(ctx, work context.Context, log *slog.Logger, job Claimed, measured *stages, err error) error {
	defer j.terminate(ctx, log, job)
	cause := context.Cause(work)
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	switch {
	case err == nil:
		log.Info("video ready", measured.logAttrs()...)
		if j.Measured != nil {
			j.Measured(job.ID, measured.List())
		}
		return nil
	case errors.Is(err, errCancelled) || errors.Is(cause, errCancelled):
		log.Info("the video was cancelled; stopped", measured.logAttrs()...)
		return j.Rows.Cancelled(settle, job)
	case errors.Is(err, errSuperseded) || errors.Is(cause, errSuperseded):
		log.Info("the attempt moved on while it was being made; stopped")
		return nil
	case errors.Is(ctx.Err(), context.Canceled):
		log.Warn("the worker is stopping; the video goes back to the queue")
		if releaseErr := j.Rows.Release(settle, job); releaseErr != nil {
			log.Error("the attempt could not be given back; it will be taken over when its heartbeat is stale", "error", releaseErr.Error())
		}
		return fmt.Errorf("%w: %w", broker.ErrRequeue, err)
	}
	code := codeOf(err)
	internal := diagnostic(err)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// The job's own deadline, not one call's.
		code, internal = codeRender, diagnostic(fmt.Errorf("the video took longer than this worker allows and was stopped: %w", err))
	}
	log.Warn("the video could not be made", append([]any{"code", code, "error", internal}, measured.logAttrs()...)...)
	if markErr := j.Rows.Fail(settle, job, code, internal); markErr != nil {
		return fmt.Errorf("%w; and the job could not be marked failed: %w", err, markErr)
	}
	return err
}

// terminate removes the attempt's sandbox. A failure is logged: the server
// removes an idle sandbox by itself when its time to live passes.
func (j *Job) terminate(ctx context.Context, log *slog.Logger, job Claimed) {
	removal, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := j.Sandboxes.TerminateJob(removal, job.sandboxName()); err != nil {
		log.Warn("the attempt's sandbox could not be removed; it expires on its own", "error", err.Error())
	}
}

// beat records the phase. A row that is no longer this attempt's stops the
// work; a database that does not answer does not (the watch and the locked
// save decide), so a blip does not cost a ten-minute render.
func (j *Job) beat(ctx context.Context, log *slog.Logger, job Claimed, phase string, progress int) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	ours, err := j.Rows.Heartbeat(ctx, job, phase, progress)
	if err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		log.Warn("the phase could not be recorded; carrying on", "phase", phase, "error", err.Error())
		return nil
	}
	if !ours {
		return j.why(ctx, job)
	}
	return nil
}

// why is the reason an attempt's write was refused: a cancel, or the
// attempt having moved on.
func (j *Job) why(ctx context.Context, job Claimed) error {
	status, _, err := j.Rows.State(context.WithoutCancel(ctx), job)
	if err == nil && (status == "cancelling" || status == "cancelled") {
		return errCancelled
	}
	return errSuperseded
}

// jobOf reads the message's one keyword argument. JSON has one number type,
// so the id arrives as a float64 and is taken only when it is a whole,
// positive number.
func jobOf(msg *celery.Message) (int64, error) {
	raw, found := msg.Kwargs["job_id"]
	number, isNumber := raw.(float64)
	if !found || len(msg.Args) != 0 || len(msg.Kwargs) != 1 || !isNumber || number < 1 || number != math.Trunc(number) || number > math.MaxInt64/2 {
		return 0, fmt.Errorf("videojob: %s takes one keyword argument, job_id; got %v and %v", Task, msg.Args, msg.Kwargs)
	}
	return int64(number), nil
}

// gate is the worker's render slots, made once.
func (j *Job) gate() chan struct{} {
	j.once.Do(func() { j.renders = make(chan struct{}, max(1, j.Config.MaxRenders)) })
	return j.renders
}
