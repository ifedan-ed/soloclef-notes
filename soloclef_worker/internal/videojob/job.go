// Package videojob makes a video: a narrated, animated slide deck.
//
// # What a video is made of
//
// The account's chat model writes the video as a slide deck with narration
// (deckspec: what each slide says and what is spoken over it, in one call;
// author.go). Its narration is voiced through the proxy's speech route, a
// slide at a time, and measured (narration.go). The slides are laid out by
// the deck renderer (deckrender), the same one that draws a slide deck,
// and each part of each slide comes in as its narration reaches it
// (film.go): only the frames in which something moves are drawn, as SVG
// rasterised by resvg (frames.go), and ffmpeg joins them, held to the
// narration, with the voice (encode.go). The MP4 is checked (verify.go) and
// kept as an artifact with its slides and their narration (deliver.go).
//
// It replaces a pipeline in which the model wrote a Remotion (React) scene
// per slide as code, which was built, checked for layout and repaired in a
// sandbox, and rendered frame by frame in a headless browser: 80 to 170
// seconds of rendering a segment, twelve minutes for a short video, and a
// video lost whenever a scene would not build. Nothing here runs code a
// model wrote, so there is no sandbox, no repair and no browser.
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
//   - A worker that is stopping waits for the video within its shutdown
//     grace (internal/broker, ShutdownGrace); one still being made at the
//     end of it gives its attempt back to the queue (Release) and the
//     message goes back with it, for the next worker. A worker that died
//     leaves a running row whose heartbeat goes stale; when its message is
//     delivered again, the claim takes the attempt over. Releases and
//     takeovers are counted together: an attempt is made again twice at
//     most, then failed (a release fails it as interrupted).
//     The Python tier needed its minute-by-minute reconciler for both, and
//     the Go stack schedules nothing.
package videojob

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os/exec"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"

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

// Notifier tells the video's owner it is ready or failed
// (soloclef_notify/notify).
type Notifier interface {
	StudioFinished(ctx context.Context, run notify.StudioRun) error
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
	// NarrationConcurrency is how many of a video's passages are voiced
	// at once, across its slides.
	NarrationConcurrency int
}

// Job runs deliverables.execute_queued messages.
type Job struct {
	Rows   Rows
	Models Models
	// Raster draws a frame (deckrender.Rasteriser over the configured
	// resvg).
	Raster  Rasteriser
	Voicer  Voicer
	Audio   Audio
	Files   filestore.Writer
	Indexer Indexer
	// Notifier is told when a video is ready or has failed; never when it
	// was cancelled or the attempt moved on.
	Notifier Notifier
	Config   Config
	Timing   Timing
	Log      *slog.Logger
	// Measured, when set, is told each delivered video's stages. The log
	// line says the same; this is for a test to read.
	Measured func(jobID int64, stages []Stage)

	once    sync.Once
	renders chan struct{}
}

// Check reports what is missing. Called once at start-up.
func (j *Job) Check() error {
	switch {
	case j.Rows == nil || j.Raster == nil || j.Models == nil || j.Voicer == nil || j.Audio == nil ||
		j.Files == nil || j.Indexer == nil || j.Notifier == nil || j.Log == nil:
		return errors.New("videojob: a dependency is missing")
	case !j.Config.Enabled:
		return errors.New("videojob: the video is not enabled; register Refusal instead")
	case j.Config.MaxRenders < 1 || j.Config.FrameWorkers < 1 || j.Config.RenderTimeout <= 0:
		return errors.New("videojob: the render limits are not set")
	case j.Timing.CallTimeout <= 0 || j.Timing.StatusInterval <= 0 || j.Timing.KeepAlive <= 0 ||
		j.Timing.Claim.StaleAfter < 3*j.Timing.KeepAlive || j.Timing.Claim.MaxTakeovers < 0 ||
		j.Timing.NarrationConcurrency < 1:
		return errors.New("videojob: the timing is incomplete, or a takeover would not wait for three missed keep-alives")
	}
	// The image's ffmpeg and ffprobe join and check every video: a worker
	// without them would fail each one after its narration was paid for.
	for _, program := range []string{ffmpegProgram, ffprobeProgram} {
		if _, err := exec.LookPath(program); err != nil {
			return fmt.Errorf("videojob: %s is not installed: %w", program, err)
		}
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
		log.Info("a cancel whose worker had stopped is finished")
		return j.Rows.Cancelled(context.WithoutCancel(ctx), job)
	}
	if job.Takeovers > 0 {
		log.Warn("taking over an attempt whose worker stopped answering", "takeover", job.Takeovers)
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

// conclude settles the row after an attempt.
func (j *Job) conclude(ctx, work context.Context, log *slog.Logger, job Claimed, measured *stages, err error) error {
	cause := context.Cause(work)
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	switch {
	case err == nil:
		log.Info("video ready", measured.logAttrs()...)
		if j.Measured != nil {
			j.Measured(job.ID, measured.List())
		}
		j.notify(settle, log, job, false)
		return nil
	case errors.Is(err, errCancelled) || errors.Is(cause, errCancelled):
		log.Info("the video was cancelled; stopped", measured.logAttrs()...)
		return j.Rows.Cancelled(settle, job)
	case errors.Is(err, errSuperseded) || errors.Is(cause, errSuperseded):
		log.Info("the attempt moved on while it was being made; stopped")
		return nil
	case errors.Is(ctx.Err(), context.Canceled):
		released, releaseErr := j.Rows.Release(settle, job, j.Timing.Claim)
		switch {
		case releaseErr != nil:
			log.Error("the attempt could not be given back; it will be taken over when its heartbeat is stale", "error", releaseErr.Error())
		case !released:
			// Stopped on every run it is allowed: it ends here, and the
			// message is acknowledged rather than put back.
			log.Warn("the worker is stopping, and this video has been restarted as often as it may; failed", "restarts", job.Takeovers)
			j.notify(settle, log, job, true)
			return nil
		default:
			log.Warn("the worker is stopping; the video goes back to the queue")
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
	j.notify(settle, log, job, true)
	return err
}

// notify tells the owner how the video ended. The failure's reason stays
// on the job's row, where the Studio's card turns its code into a
// sentence; the notification says that it failed and opens the card. A
// notification that cannot be written is logged and the video's ending
// stands.
func (j *Job) notify(ctx context.Context, log *slog.Logger, job Claimed, failed bool) {
	if err := j.Notifier.StudioFinished(ctx, notify.StudioRun{UserID: job.UserID, JobID: job.ID, Failed: failed}); err != nil {
		log.Error("the video's notification could not be written", "error", err.Error())
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
