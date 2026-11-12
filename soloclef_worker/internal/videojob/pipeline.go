package videojob

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"

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

	if err := beat("preparing", 5); err != nil {
		return err
	}
	stop := measured.start("sandbox")
	box, err := j.Sandboxes.ForJob(ctx, job.UserID, job.sandboxName())
	if err != nil {
		return fail(codeRender, fmt.Errorf("could not get the video's sandbox: %w", err))
	}
	h := harness{box: box, workdir: "/workspace/" + job.sandboxName(), config: j.Config,
		short: j.Config.Sandbox.CommandTimeout, long: j.Config.RenderTimeout, renders: j.gate()}
	output := h.workdir + ".mp4"
	if err := h.prepare(ctx); err != nil {
		return fail(codeRender, err)
	}
	stop()

	if err := beat("authoring", 10); err != nil {
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
	stop = measured.start("authoring")
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
	n := narrator{voicer: j.Voicer, measure: j.Audio, voice: j.Config.Voice,
		instructions: j.Config.NarrationInstructions, concurrency: j.Timing.NarrationConcurrency}
	stop = measured.start("narration")
	voices, total, err := n.narrate(ctx, video)
	if err != nil {
		return fail(codeGeneration, err)
	}
	if total > float64(limit) {
		// Measured overrun: one rewrite of the narration, one more voicing.
		words := countWords(video.transcripts())
		target := overrunWords(words, total, limit)
		video, err = w.shorten(ctx, video, map[string]any{
			"reason": "synthesized narration is too long", "measured_seconds": math.Round(total*10) / 10,
			"limit_seconds": limit, "required_length_ratio": math.Round(float64(target)/float64(words)*1000) / 1000,
			"current_words": words, "target_words": target,
		})
		if err != nil {
			return fail(codeGeneration, err)
		}
		if err := beat("narrating", 30); err != nil {
			return err
		}
		if voices, total, err = n.narrate(ctx, video); err != nil {
			return fail(codeGeneration, err)
		}
		if total > float64(limit) {
			return fail(codeDuration, fmt.Errorf("the narration runs %.1fs, over the %d-second limit, after it was shortened", total, limit))
		}
	}
	if err := h.writeNarration(ctx, voices); err != nil {
		return fail(codeRender, err)
	}
	stop()

	// One budget of repairs for every kind of finding. The Python worker
	// allowed one repair before the render and the rest after, and a
	// repair after a failed check that then failed the build ended the job
	// with repairs still in hand.
	repairs := 0
	repair := func(findings string) error {
		if err := beat("repairing", min(93, 55+10*repairs)); err != nil {
			return err
		}
		done := measured.start("repair")
		defer done()
		repaired, err := w.repair(ctx, video, findings)
		if err != nil {
			return fail(codeGeneration, err)
		}
		video = repaired
		repairs++
		return nil
	}
	var checked verified
	numbers := make([]int, 0, len(video.Scenes))
	for _, scene := range video.Scenes {
		numbers = append(numbers, scene.Number)
	}
	for {
		if err := beat("preflighting", min(60, 40+8*repairs)); err != nil {
			return err
		}
		props, err := h.writeProps(ctx, video)
		if err != nil {
			return fail(codeRender, err)
		}
		stop = measured.start("preflight")
		issue, err := h.preflight(ctx, props)
		stop()
		if err != nil {
			return fail(codeRender, fmt.Errorf("could not build the scenes: %w", err))
		}
		if issue == "" {
			if err := beat("reviewing", min(62, 50+6*repairs)); err != nil {
				return err
			}
			// The stills step renders each scene's last frame and checks
			// its layout (layout.go); a scene that does not render is an
			// issue of its own.
			stop = measured.start("stills")
			issue, err = h.stills(ctx, props)
			if err == nil && issue == "" {
				var findings []string
				findings, err = readLayout(ctx, box, h.stillsDir(), numbers)
				issue = strings.Join(findings, "; ")
			}
			stop()
			if err != nil {
				return fail(codeRender, fmt.Errorf("could not check the scenes' layout: %w", err))
			}
		}
		if issue != "" {
			if repairs >= j.Timing.RepairCycles {
				return fail(codeRender, fmt.Errorf("the scenes still failed their checks after %d repairs: %s", repairs, tail(issue, 1000)))
			}
			if err := repair(issue); err != nil {
				return err
			}
			continue
		}

		if err := beat("rendering", min(90, 65+10*repairs)); err != nil {
			return err
		}
		stop = measured.start("render")
		expected := 0.0
		if j.Config.Mode == ModeStills {
			var grid timeline
			grid, err = h.assembleFromStills(ctx, props, voices, output)
			expected, _ = grid.seconds().Float64()
		} else {
			err = h.renderFrames(ctx, props, output)
		}
		stop()
		if err != nil {
			return fail(codeRender, err)
		}
		if err := beat("verifying", min(94, 85+3*repairs)); err != nil {
			return err
		}
		stop = measured.start("verify")
		checked, err = verifyVideo(ctx, box, output, expected, frameSamples(voices), h.short)
		stop()
		if err != nil {
			return fail(codeRender, err)
		}
		if len(checked.Findings) == 0 {
			break
		}
		if repairs >= j.Timing.RepairCycles {
			return fail(codeVerification, fmt.Errorf("the video failed verification after %d repairs: %s", repairs, strings.Join(checked.Findings, "; ")))
		}
		if err := repair(strings.Join(checked.Findings, "; ")); err != nil {
			return err
		}
	}

	if err := beat("saving", 95); err != nil {
		return err
	}
	stop = measured.start("save")
	defer stop()
	return j.deliver(ctx, log, job, box, output, video, voices, checked)
}

// fitWords shortens a draft that is over its word budget before anything
// is voiced. A word count only estimates the spoken length, so a model that
// cannot be read here leaves the draft as it is, and the measured length
// after voicing decides (it has its own shortening).
func (j *Job) fitWords(ctx context.Context, log *slog.Logger, w author, video Authored, limit int, beat func(string, int) error) (Authored, error) {
	budget := narrationWordBudget(limit)
	for attempt := 0; attempt < draftShortenings; attempt++ {
		words := countWords(video.transcripts())
		if words <= budget {
			break
		}
		if err := beat("authoring", 15+5*attempt); err != nil {
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
