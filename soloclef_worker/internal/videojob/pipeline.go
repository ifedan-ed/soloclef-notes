package videojob

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/llm"
)

// make is one attempt, from the request to the saved video.
func (j *Job) make(ctx context.Context, log *slog.Logger, job Claimed, measured *stages) error {
	request, err := readRequest(job.Request)
	if err != nil {
		return fail(codeGeneration, err)
	}
	switch {
	case job.Kind != "video":
		return fail(codeGeneration, fmt.Errorf("a job of kind %q is not a video; nothing in this worker makes it", job.Kind))
	case job.ThreadID == nil:
		return fail(codeGeneration, errors.New("the conversation the video was asked for in is gone"))
	case *job.ThreadID != request.RootThreadID:
		return fail(codeGeneration, fmt.Errorf("the request names conversation %d and the job belongs to %d", request.RootThreadID, *job.ThreadID))
	}
	limit := request.limitSeconds()
	beat := func(phase string, progress int) error { return j.beat(ctx, log, job, phase, progress) }

	if err := beat("authoring", 5); err != nil {
		return err
	}
	name, err := j.Models.StudioModel(ctx, job.UserID)
	if err != nil {
		return fail(codeGeneration, err)
	}
	writer, err := j.Models.Client(ctx, job.UserID, name)
	if err != nil {
		return fail(codeGeneration, err)
	}
	w := author{model: writer, timeout: j.Timing.CallTimeout}
	stop := measured.start("authoring")
	video, err := w.write(ctx, job.Title, request)
	if err != nil {
		return fail(codeGeneration, err)
	}
	if video, err = j.fitWords(ctx, log, w, video, limit, beat); err != nil {
		return err
	}
	stop()

	if err := beat("narrating", 25); err != nil {
		return err
	}
	n := narrator{voicer: j.Voicer, audio: j.Audio, voice: j.Config.Voice,
		instructions: j.Config.NarrationInstructions, concurrency: j.Timing.NarrationConcurrency}
	stop = measured.start("narration")
	voices, total, err := n.narrate(ctx, video)
	if err != nil {
		return fail(codeGeneration, err)
	}
	if total > float64(limit) {
		// Measured overrun: one rewrite of the narration, one more voicing.
		words := countWords(video.spoken())
		target := overrunWords(words, total, limit)
		video, err = w.shorten(ctx, video, map[string]any{
			"reason": "synthesized narration is too long", "measured_seconds": math.Round(total*10) / 10,
			"limit_seconds": limit, "required_length_ratio": math.Round(float64(target)/float64(words)*1000) / 1000,
			"current_words": words, "target_words": target,
		})
		if err != nil {
			return fail(codeGeneration, err)
		}
		if err := beat("narrating", 35); err != nil {
			return err
		}
		if voices, total, err = n.narrate(ctx, video); err != nil {
			return fail(codeGeneration, err)
		}
		if total > float64(limit) {
			return fail(codeDuration, fmt.Errorf("the narration runs %.1fs, over the %d-second limit, after it was shortened", total, limit))
		}
	}
	stop()

	if err := beat("rendering", 50); err != nil {
		return err
	}
	shots, err := plan(video, voices)
	if err != nil {
		return fail(codeRender, err)
	}
	dir, err := os.MkdirTemp("", "soloclef-video-*")
	if err != nil {
		return fail(codeRender, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	output := filepath.Join(dir, "video.mp4")
	if err := j.render(ctx, measured, video, voices, shots, dir, output); err != nil {
		return err
	}

	if err := beat("verifying", 90); err != nil {
		return err
	}
	stop = measured.start("verify")
	expected := float64(totalFrames(shots)) / fps
	checked, err := verifyVideo(ctx, mediaTools{ffmpeg: ffmpegProgram, ffprobe: ffprobeProgram}, output, expected, frameSamples(shots))
	stop()
	if err != nil {
		return fail(codeRender, err)
	}
	if len(checked.Findings) > 0 {
		// Nothing a model wrote can make a drawn video fail these: a
		// finding is a fault in the drawing or the join, said as it is.
		return fail(codeVerification, fmt.Errorf("the video failed verification: %v", checked.Findings))
	}

	if err := beat("saving", 95); err != nil {
		return err
	}
	stop = measured.start("save")
	defer stop()
	return j.deliver(ctx, log, job, output, video, voices, shots, checked)
}

// render draws the film's frames and joins them with the narration, within
// one of this worker's render slots and the render's time limit.
func (j *Job) render(ctx context.Context, measured *stages, video Authored, voices []voiced, shots []shot, dir, output string) error {
	select {
	case j.gate() <- struct{}{}:
		defer func() { <-j.gate() }()
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	bounded, cancel := context.WithTimeout(ctx, j.Config.RenderTimeout)
	defer cancel()
	film := cuts(video, shots)
	stop := measured.start("frames")
	pictures, err := draw(bounded, j.Raster, j.Config.FrameWorkers, dir, video, shots, film)
	stop()
	if err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return fail(codeRender, fmt.Errorf("drawing the frames: %w", err))
	}
	stop = measured.start("encode")
	err = join(bounded, ffmpegProgram, dir, j.Config.FrameWorkers, film, pictures, shots, voices, output)
	stop()
	if err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return fail(codeRender, err)
	}
	return nil
}

// fitWords shortens a draft that is over its word budget before anything
// is voiced. A word count only estimates the spoken length, so a model that
// cannot be read here leaves the draft as it is, and the measured length
// after voicing decides (it has its own shortening).
func (j *Job) fitWords(ctx context.Context, log *slog.Logger, w author, video Authored, limit int, beat func(string, int) error) (Authored, error) {
	budget := narrationWordBudget(limit)
	for attempt := 0; attempt < draftShortenings; attempt++ {
		words := countWords(video.spoken())
		if words <= budget {
			break
		}
		if err := beat("authoring", 12+5*attempt); err != nil {
			return Authored{}, err
		}
		shorter, err := w.shorten(ctx, video, map[string]any{
			"reason": "narration is over the word budget", "limit_seconds": limit,
			"current_words": words, "target_words": budget,
		})
		if errors.Is(err, llm.ErrUnusable) {
			log.Warn("the narration could not be shortened before voicing; the measured length will decide", "error", err.Error())
			break
		}
		if err != nil {
			return Authored{}, fail(codeGeneration, err)
		}
		video = shorter
	}
	return video, nil
}
