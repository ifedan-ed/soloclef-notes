package videojob

import (
	"context"
	"fmt"
	"math"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// Voicer speaks one line through the proxy (speech.Client): MP3 bytes.
type Voicer interface {
	Speak(ctx context.Context, line speech.Line) ([]byte, error)
}

// Measurer says how long an MP3 plays (audiofile.Tools).
type Measurer interface {
	Seconds(ctx context.Context, data []byte) (float64, error)
}

// voiced is one scene's narration.
type voiced struct {
	Number  int
	MP3     []byte
	Seconds float64
}

// narrator voices every scene's transcript in the configured voice.
//
// The voice is one setting (SOLOCLEF_WORKER_VIDEO_NARRATION_VOICE). The
// Python worker chose one from a catalogue by the language the author
// declared, falling back through a default and a legacy voice; here the
// deployment names the voice it narrates in, and a voice the service does
// not have stops the worker at start-up instead.
type narrator struct {
	voicer       Voicer
	measure      Measurer
	voice        any
	instructions string
	// concurrency is how many scenes are voiced at once. The Python worker
	// voiced every scene of a video at the same moment, twelve requests to
	// a provider that rate-limits at fewer.
	concurrency int
}

// narrate voices every scene and measures each. It returns them in scene
// order and their total length.
func (n narrator) narrate(ctx context.Context, video Authored) ([]voiced, float64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := make([]voiced, len(video.Scenes))
	// The first failure is the one reported; the scenes stopped because of
	// it fail only with the cancel it caused.
	var (
		once  sync.Once
		first error
	)
	failed := func(err error) {
		once.Do(func() { first = err })
		cancel()
	}
	slots := make(chan struct{}, max(1, n.concurrency))
	var wait sync.WaitGroup
	for i, scene := range video.Scenes {
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				failed(ctx.Err())
				return
			}
			defer func() { <-slots }()
			mp3, err := n.voicer.Speak(ctx, speech.Line{Text: scene.Transcript, Voice: n.voice, Instructions: n.instructions})
			if err != nil {
				failed(fmt.Errorf("voicing scene %d: %w", scene.Number, err))
				return
			}
			seconds, err := n.measure.Seconds(ctx, mp3)
			if err == nil && (seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0)) {
				err = fmt.Errorf("it measured %v seconds", seconds)
			}
			if err != nil {
				failed(fmt.Errorf("measuring scene %d's narration: %w", scene.Number, err))
				return
			}
			out[i] = voiced{Number: scene.Number, MP3: mp3, Seconds: seconds}
		}()
	}
	wait.Wait()
	if first != nil {
		return nil, 0, first
	}
	total := 0.0
	for _, voice := range out {
		total += voice.Seconds
	}
	return out, total, nil
}
