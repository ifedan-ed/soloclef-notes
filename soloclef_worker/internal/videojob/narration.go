package videojob

import (
	"context"
	"fmt"
	"math"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// Voicer speaks one line through the proxy (speech.Client): MP3 bytes. It
// retries a failure that may pass (a rate limit, a gateway fault, an
// answer cut short) on its own, up to SOLOCLEF_WORKER_TTS_MAX_ATTEMPTS.
type Voicer interface {
	Speak(ctx context.Context, line speech.Line) ([]byte, error)
}

// Audio is what the narration does to the voiced bytes (audiofile.Tools):
// measures how long an MP3 plays, and joins a slide's passages into one
// recording with a pause between each two.
type Audio interface {
	Seconds(ctx context.Context, data []byte) (float64, error)
	JoinSpaced(ctx context.Context, parts [][]byte, gap float64) ([]byte, error)
}

// voiced is one slide's narration.
type voiced struct {
	Number int
	// MP3 is the slide's passages joined, which the video lays under the
	// slide and the artifact stores as the slide's audio.
	MP3     []byte
	Seconds float64
	// Starts is when each passage begins, in seconds into MP3, one per
	// build step of the slide (deckspec.Steps): measured from the voiced
	// passages, so each part comes in as its passage really starts.
	Starts []float64
}

// narrator voices every passage of every slide in the configured voice,
// one speech call a passage, and joins each slide's passages into its
// narration.
//
// A passage is voiced on its own so that where it starts is known. One
// call for a whole slide gave no passage boundaries (the speech service
// returns no word timings), and estimating them from the passages' share
// of the words drifted by seconds wherever the voice slowed for a number
// said in words, an abbreviation or a sentence's pause. Kokoro, the voice
// this was measured with, already speaks a long text sentence by sentence,
// so a passage voiced alone sounds as it did inside the slide; passageGap
// is the pause it put between them.
//
// The voice is one setting (SOLOCLEF_WORKER_VIDEO_NARRATION_VOICE). The
// Python worker chose one from a catalogue by the language the author
// declared, falling back through a default and a legacy voice; here the
// deployment names the voice it narrates in, and a voice the service does
// not have stops the worker at start-up instead.
type narrator struct {
	voicer       Voicer
	audio        Audio
	voice        any
	instructions string
	// concurrency is how many speech calls one video has open at once
	// (SOLOCLEF_WORKER_TTS_CONCURRENCY), across all its slides' passages:
	// a 20-slide video of 4 passages a slide is 80 calls, and the provider
	// rate-limits well below that. The Python worker voiced every scene of
	// a video at the same moment.
	concurrency int
}

// passageGap is the silence added between two passages of a slide, in
// seconds: none, because the voice brings its own. Kokoro (af_heart) ends a
// passage voiced alone in about 0.37 s of silence and starts one with about
// 0.14 s, so two passages laid end to end pause before the next one's words
// a median 0.53 s. Inside one call for the whole slide it paused 0.51 s
// there (job 29's clinical narration, 54 boundaries) and 0.41 s (the
// clinical test deck's short passages, 29): the passages' own silence is
// already the voice's pace, and a 0.12 s gap, tried first, stretched every
// boundary to 0.65 s. A voice whose passages end without silence would want
// one; this is the place to say so.
const passageGap = 0.0

// narrate voices every slide and measures each. It returns them in slide
// order and their total length.
func (n narrator) narrate(ctx context.Context, video Authored) ([]voiced, float64, error) {
	slides := video.Deck.Slides
	parts := make([][][]byte, len(slides))
	lengths := make([][]float64, len(slides))
	var calls []func(context.Context) error
	for i, s := range slides {
		parts[i] = make([][]byte, len(s.Narration))
		lengths[i] = make([]float64, len(s.Narration))
		for k, text := range s.Narration {
			calls = append(calls, func(ctx context.Context) error {
				mp3, seconds, err := n.passage(ctx, text)
				if err != nil {
					return fmt.Errorf("slide %d's passage %d: %w", i+1, k+1, err)
				}
				parts[i][k], lengths[i][k] = mp3, seconds
				return nil
			})
		}
	}
	// Every passage of the video in one bounded pool, not slide by slide:
	// a slide's calls do not wait for the slowest call of the slide
	// before it.
	if err := runAll(ctx, n.concurrency, calls); err != nil {
		return nil, 0, err
	}

	out := make([]voiced, len(slides))
	joins := make([]func(context.Context) error, len(slides))
	for i := range slides {
		joins[i] = func(ctx context.Context) error {
			slide, err := n.join(ctx, i+1, parts[i], lengths[i])
			out[i] = slide
			return err
		}
	}
	// The joins are an ffmpeg process each; the same bound keeps a long
	// video from starting twenty at once.
	if err := runAll(ctx, n.concurrency, joins); err != nil {
		return nil, 0, err
	}
	total := 0.0
	for _, voice := range out {
		total += voice.Seconds
	}
	return out, total, nil
}

// passage voices one passage and measures it.
func (n narrator) passage(ctx context.Context, text string) ([]byte, float64, error) {
	mp3, err := n.voicer.Speak(ctx, speech.Line{Text: text, Voice: n.voice, Instructions: n.instructions})
	if err != nil {
		return nil, 0, fmt.Errorf("voicing it: %w", err)
	}
	seconds, err := n.measured(ctx, mp3)
	if err != nil {
		return nil, 0, fmt.Errorf("measuring it: %w", err)
	}
	return mp3, seconds, nil
}

// join makes one slide's narration from its passages, and says when each
// starts in it.
func (n narrator) join(ctx context.Context, number int, parts [][]byte, lengths []float64) (voiced, error) {
	mp3, err := n.audio.JoinSpaced(ctx, parts, passageGap)
	if err != nil {
		return voiced{}, fmt.Errorf("joining slide %d's passages: %w", number, err)
	}
	seconds, err := n.measured(ctx, mp3)
	if err != nil {
		return voiced{}, fmt.Errorf("measuring slide %d's narration: %w", number, err)
	}
	return voiced{Number: number, MP3: mp3, Seconds: seconds, Starts: passageStarts(lengths, passageGap)}, nil
}

// measured is an MP3's length, refused when it is no length at all.
func (n narrator) measured(ctx context.Context, mp3 []byte) (float64, error) {
	seconds, err := n.audio.Seconds(ctx, mp3)
	if err == nil && (seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0)) {
		err = fmt.Errorf("it measured %v seconds", seconds)
	}
	return seconds, err
}

// passageStarts is when each passage begins in a slide's narration: the
// lengths of the passages before it and a gap after each of them, as
// audiofile.JoinSpaced lays them.
func passageStarts(lengths []float64, gap float64) []float64 {
	starts := make([]float64, len(lengths))
	at := 0.0
	for k, seconds := range lengths {
		starts[k] = at
		at += seconds + gap
	}
	return starts
}
