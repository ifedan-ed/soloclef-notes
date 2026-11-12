// Package podcast is the podcast job: the transcript drafted from the
// approved brief, then voiced, joined into one MP3 and kept as the
// podcast's artifact.
//
// # The message
//
// `podcast.draft_transcript`, one positional argument: the podcast's id.
// The API tier queues it when a brief is approved (internal/podcasts,
// action_route.go), after the row says `drafting`. Whose podcast it is, is
// read off the row.
//
// # One job, two phases
//
// The Python tier drafted in one task and queued a second to render. Here
// the job renders as soon as the transcript is stored, in the same run: a
// second message would be one more thing to lose between the two, and the
// render reads its lines from the cache the draft has just filled. The
// phase is decided by the row, not the message: a job delivered again
// after a restart finds `rendering` and renders, starting from the lines
// already voiced.
//
// # Cancelling
//
// A podcast can be cancelled (or sent back to its brief) while the job
// runs. The row is checked between drafting calls and before each line is
// voiced, so the work stops within moments instead of running to the end
// (the Python tasks checked once, at the end, after paying for all of it).
// And the two writes that matter, the transcript and the finished episode,
// re-read the status under a row lock held to the commit (store.go,
// lockStatus), so a cancel is never written over.
package podcast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/llm"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// Task is the message's name, which the API tier publishes and
// soloclef_celery/routing sends to the heavy queue.
const Task = "podcast.draft_transcript"

// Models gives the job a model to draft with.
type Models interface {
	// StudioModel is the stored name of the model the account's long
	// written work is drafted by (llm.Choices): a proxy name, or a model of
	// the account's own connection.
	StudioModel(ctx context.Context, userID string) (string, error)
	// Client is a client for that model, for that account.
	Client(ctx context.Context, userID, name string) (model.Model, error)
}

// Audio joins and measures the episode (audiofile.Tools).
type Audio interface {
	JoinMP3(ctx context.Context, segments []string, output string) error
	Duration(ctx context.Context, path string) (float64, error)
}

// Indexer asks for an artifact's document to be made searchable.
type Indexer interface {
	Index(ctx context.Context, documentID int64) error
}

// Config is the job's own settings.
type Config struct {
	// CallTimeout bounds one drafting call.
	CallTimeout time.Duration
	// Speech is the configured speech service; a brief's voices must be
	// its roster's.
	Speech speech.Service
	// Instructions is the deployment's speaking style, or "". A speaker's
	// own delivery note wins over it.
	Instructions string
	// Concurrency is how many lines one podcast voices at once. A hosted
	// voice spends most of a line waiting on the provider, so eight in
	// flight roughly halves a long episode's render against four.
	Concurrency int
	// WorkDirectory holds one directory per podcast while it is made: the
	// voiced lines and the joined episode. Each is removed when its
	// podcast is finished, failed or cancelled.
	WorkDirectory string
	// StatusInterval is how long the job may go without asking whether
	// the podcast was cancelled.
	StatusInterval time.Duration
}

// Job runs podcast.draft_transcript messages.
type Job struct {
	Rows    Rows
	Models  Models
	Voicer  Voicer
	Audio   Audio
	Indexer Indexer
	Config  Config
	Log     *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (j *Job) Check() error {
	switch {
	case j.Rows == nil || j.Models == nil || j.Voicer == nil || j.Audio == nil || j.Indexer == nil || j.Log == nil:
		return errors.New("podcast: a dependency is missing")
	case j.Config.CallTimeout <= 0 || j.Config.Concurrency < 1 || j.Config.StatusInterval <= 0:
		return errors.New("podcast: the call timeout, the concurrency and the status interval must all be set")
	case j.Config.WorkDirectory == "" || j.Config.Speech.Model == "":
		return errors.New("podcast: no working directory or no speech service")
	}
	return nil
}

// errSuperseded is a podcast that moved on while the job worked: cancelled,
// sent back to its brief, deleted. Nothing more is to be done, and nothing
// is wrong.
var errSuperseded = errors.New("podcast: the podcast moved on while it was being made")

// failure is a job that cannot finish, with what the person is told.
type failure struct {
	reason string
	err    error
}

func (f *failure) Error() string { return f.reason + ": " + f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

func failed(reason string, err error) error {
	if err == nil || errors.Is(err, errSuperseded) {
		return err
	}
	var already *failure
	if errors.As(err, &already) {
		return err
	}
	return &failure{reason: reason, err: err}
}

// What a person is told when a podcast fails. The technical error goes to
// the log; the Python tasks stored it on the row, and the page showed a
// stack's worth of provider text.
const (
	draftFailed   = "The transcript could not be written. Try again; if it keeps failing, try another chat model."
	voiceFailed   = "The audio could not be made because the speech service did not answer. Try again in a few minutes."
	keepFailed    = "The finished episode could not be saved. Try again."
	briefFailed   = "The podcast's brief could not be read. Edit the brief and approve it again."
	timeoutFailed = "The podcast took longer than this server allows and was stopped."
	otherFailed   = "The podcast could not be made. Try again."
)

// Run is the jobs.Handler.
func (j *Job) Run(ctx context.Context, msg *celery.Message) error {
	podcastID, err := podcastOf(msg)
	if err != nil {
		return err
	}
	run, err := j.Rows.Load(ctx, podcastID)
	if errors.Is(err, ErrGone) {
		j.Log.Info("the podcast is gone; nothing to make", "podcast", podcastID)
		return nil
	}
	if err != nil {
		// The row could not be read, so there is nothing to mark either;
		// the message goes back and is tried again.
		return fmt.Errorf("%w: %w", broker.ErrRequeue, err)
	}
	log := j.Log.With("podcast", run.ID)
	workdir := filepath.Join(j.Config.WorkDirectory, strconv.FormatInt(run.ID, 10))

	switch run.Status {
	case statusDrafting:
		err = j.draftThenRender(ctx, log, run, workdir)
	case statusRendering:
		err = j.render(ctx, log, run, workdir)
	default:
		// Cancelled, failed, back at its brief, or already ready (a message
		// delivered twice).
		log.Info("nothing to make: the podcast is no longer being made", "status", run.Status)
		return nil
	}
	return j.conclude(ctx, log, run, workdir, err)
}

// conclude settles the podcast after a run. The working directory goes
// whatever happened, except when the worker itself is stopping: then the
// message goes back, and the next delivery starts from the lines already
// voiced.
func (j *Job) conclude(ctx context.Context, log *slog.Logger, run Run, workdir string, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		log.Warn("the worker is stopping; the podcast goes back to the queue")
		return fmt.Errorf("%w: %w", broker.ErrRequeue, err)
	}
	if errors.Is(err, errSuperseded) {
		log.Info("the podcast moved on while it was being made; stopped")
		// A message delivered twice is two jobs on one podcast: the one
		// that lost the race finds it still being made, by the other, whose
		// lines are in this directory. Only a podcast nobody is making any
		// more has its directory removed.
		if status, statusErr := j.Rows.Status(context.WithoutCancel(ctx), run.ID, run.UserID); statusErr == nil &&
			(status == statusDrafting || status == statusRendering) {
			return nil
		}
		os.RemoveAll(workdir)
		return nil
	}
	defer os.RemoveAll(workdir)
	if err == nil {
		return nil
	}
	reason := otherFailed
	var known *failure
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// The job's own deadline, not one call's.
		reason = timeoutFailed
	case errors.As(err, &known):
		reason = known.reason
	}
	// Not the job's context: a podcast must be told it failed even when the
	// failure is the job's deadline.
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if markErr := j.Rows.Fail(markCtx, run.ID, run.UserID, reason); markErr != nil {
		return fmt.Errorf("%w; and the podcast could not be marked failed: %w", err, markErr)
	}
	return err
}

func (j *Job) draftThenRender(ctx context.Context, log *slog.Logger, run Run, workdir string) error {
	brief, err := ReadBrief(run.Spec)
	if err != nil {
		return failed(briefFailed, err)
	}
	voices, err := newVoices(j.Voicer, j.Config.Speech, brief, j.Config.Instructions, filepath.Join(workdir, "lines"), j.Config.Concurrency)
	if err != nil {
		return failed(voiceFailed, err)
	}
	watch := &watch{rows: j.Rows, run: run, want: statusDrafting, every: j.Config.StatusInterval}

	started := time.Now()
	transcript, err := j.draft(ctx, log, run, brief, voices, watch)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(transcript)
	if err != nil {
		return failed(draftFailed, err)
	}
	attached, err := j.Rows.AttachTranscript(ctx, run.ID, run.UserID, encoded)
	if err != nil {
		return failed(draftFailed, err)
	}
	if !attached {
		return errSuperseded
	}
	log.Info("transcript drafted", "lines", len(transcript.Turns), "took", time.Since(started).Round(time.Millisecond).String())
	run.Status, run.Transcript = statusRendering, encoded
	return j.render(ctx, log, run, workdir)
}

// draft writes the transcript beat by beat. Each beat's lines are voiced
// while the next beat is written, into the cache the render reads, so the
// render mostly joins.
func (j *Job) draft(ctx context.Context, log *slog.Logger, run Run, brief Brief, voices *voices, watch *watch) (Transcript, error) {
	name, err := j.Models.StudioModel(ctx, run.UserID)
	if err != nil {
		return Transcript{}, failed(draftFailed, err)
	}
	writer, err := j.Models.Client(ctx, run.UserID, name)
	if err != nil {
		return Transcript{}, failed(draftFailed, err)
	}
	source := sourceBlock(run.Source)

	plan, err := llm.AskJSON(ctx, writer, j.Config.CallTimeout, []model.Message{
		{Role: model.RoleSystem, Content: outlinePrompt(brief)},
		{Role: model.RoleUser, Content: source},
	}, readOutline)
	if err != nil {
		return Transcript{}, failed(draftFailed, err)
	}

	// Lines voiced ahead are a head start, not a requirement: a line that
	// fails here is voiced again by the render, which is where a failure
	// counts. They stop with the draft.
	ahead, stopAhead := context.WithCancel(ctx)
	var voicing sync.WaitGroup
	var aheadFailures sync.Map
	// On every way out, the lines still being voiced are stopped and waited
	// for, so none is still writing into the directory when the job
	// removes it.
	defer func() {
		stopAhead()
		voicing.Wait()
	}()

	var turns []Turn
	for index, beat := range plan.Segments {
		if err := watch.still(ctx); err != nil {
			return Transcript{}, err
		}
		drafted, err := llm.AskJSON(ctx, writer, j.Config.CallTimeout, []model.Message{
			{Role: model.RoleSystem, Content: segmentPrompt(brief, beat, index+1, len(plan.Segments), recap(brief, turns))},
			{Role: model.RoleUser, Content: source},
		}, readTurns)
		if err != nil {
			return Transcript{}, failed(draftFailed, err)
		}
		// A line given to a slot the brief does not have is dropped, so a
		// stray attribution cannot break the render.
		var kept []Turn
		for _, turn := range drafted {
			if _, found := brief.speaker(turn.Speaker); found {
				kept = append(kept, turn)
			}
		}
		turns = append(turns, kept...)
		for _, turn := range kept {
			voicing.Add(1)
			go func() {
				defer voicing.Done()
				if _, err := voices.path(ahead, turn); err != nil && ahead.Err() == nil {
					aheadFailures.Store(turn.Text, err)
				}
			}()
		}
	}
	voicing.Wait()
	aheadFailures.Range(func(_, value any) bool {
		log.Warn("a line could not be voiced while drafting; the render will try it again", "error", value.(error).Error())
		return false
	})
	if len(turns) == 0 {
		return Transcript{}, failed(draftFailed, errors.New("the drafting produced no usable dialogue"))
	}
	return Transcript{Turns: turns}, nil
}

func (j *Job) render(ctx context.Context, log *slog.Logger, run Run, workdir string) error {
	brief, err := ReadBrief(run.Spec)
	if err != nil {
		return failed(briefFailed, err)
	}
	transcript, err := ReadTranscript(run.Transcript)
	if err != nil {
		return failed(draftFailed, err)
	}
	voices, err := newVoices(j.Voicer, j.Config.Speech, brief, j.Config.Instructions, filepath.Join(workdir, "lines"), j.Config.Concurrency)
	if err != nil {
		return failed(voiceFailed, err)
	}
	watch := &watch{rows: j.Rows, run: run, want: statusRendering, every: j.Config.StatusInterval}

	started := time.Now()
	paths, err := voices.all(ctx, transcript.Turns, watch.still)
	if err != nil {
		var refused *speech.VoiceError
		if errors.As(err, &refused) {
			// The person can act on this one: the brief names a voice the
			// server no longer speaks.
			return failed("The brief names a voice this server cannot speak ("+refused.VoiceID+"). Edit the brief and choose another voice.", err)
		}
		return failed(voiceFailed, err)
	}
	voiced := time.Since(started)
	output := filepath.Join(workdir, "episode.mp3")
	if err := j.Audio.JoinMP3(ctx, paths, output); err != nil {
		return failed(voiceFailed, err)
	}
	seconds, err := j.Audio.Duration(ctx, output)
	if err != nil {
		return failed(voiceFailed, err)
	}
	audio, err := os.ReadFile(output)
	if err != nil {
		return failed(keepFailed, err)
	}
	documentID, delivered, err := j.Rows.Deliver(ctx, Delivery{
		Run: run, Markdown: markdown(run.Title, brief, transcript), Audio: audio, Seconds: seconds,
	})
	if err != nil && documentID == 0 {
		return failed(keepFailed, err)
	}
	if err != nil {
		// Kept, and the old take's file is left over: worth a line in the
		// log, not a failed podcast.
		log.Warn("the episode is kept; tidying the previous take failed", "error", err.Error())
	}
	if !delivered {
		return errSuperseded
	}
	log.Info("podcast ready", "lines", len(transcript.Turns), "seconds", math.Round(seconds),
		"bytes", len(audio), "voiced", voiced.Round(time.Millisecond).String(),
		"joined", (time.Since(started) - voiced).Round(time.Millisecond).String())
	// The episode is kept and plays whether or not its text is searchable
	// yet, so a failure to queue the indexing does not fail the podcast.
	if err := j.Indexer.Index(context.WithoutCancel(ctx), documentID); err != nil {
		log.Error("the episode is kept and its transcript could not be queued for indexing", "document", documentID, "error", err.Error())
	}
	return nil
}

// watch asks, at most every so often, whether the podcast is still where
// the job expects it.
type watch struct {
	rows  Rows
	run   Run
	want  string
	every time.Duration

	mu     sync.Mutex
	asked  time.Time
	result error
}

func (w *watch) still(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.result != nil || time.Since(w.asked) < w.every {
		return w.result
	}
	status, err := w.rows.Status(ctx, w.run.ID, w.run.UserID)
	w.asked = time.Now()
	switch {
	case errors.Is(err, ErrGone):
		w.result = errSuperseded
	case err != nil:
		// The database did not answer the question; the work goes on, and
		// the locked write at the end decides.
		return nil
	case status != w.want:
		w.result = errSuperseded
	}
	return w.result
}

// podcastOf reads the one argument. JSON has one number type, so the id
// arrives as a float64 and is taken only when it is a whole, positive
// number.
func podcastOf(msg *celery.Message) (int64, error) {
	if len(msg.Args) != 1 || len(msg.Kwargs) != 0 {
		return 0, fmt.Errorf("podcast: %s takes one argument, the podcast id; got %d positional and %d keyword",
			Task, len(msg.Args), len(msg.Kwargs))
	}
	number, isNumber := msg.Args[0].(float64)
	if !isNumber || number < 1 || number > math.MaxInt64/2 || number != math.Trunc(number) {
		return 0, fmt.Errorf("podcast: %v is not a podcast id", msg.Args[0])
	}
	return int64(number), nil
}
