package videojob

import (
	"fmt"
	"math"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"
	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"
)

// The film: when each slide is on screen, when each of its parts comes in,
// and which frames must be drawn. Only the frames in which something moves
// are drawn; between them the last picture is held for as many frames as
// the narration needs. A slide's parts take a second or so each to come in
// and then sit still while they are talked about, so a three-minute video
// is a few hundred drawn frames, not five thousand four hundred: that, and
// drawing them with resvg instead of a browser, is where the time goes.

const (
	fps = 30
	// leadIn is how long a slide is up before its narration starts: the
	// title is in before it is talked about.
	leadIn = 0.4
	// tailOut is the pause after a slide's narration before the next one.
	tailOut = 0.7
	// earlyBy is how far ahead of its passage a part starts to come in, so
	// it has arrived as it is being talked about.
	earlyBy = 0.25
	// settleMargin is how long a slide's last picture stands after its last
	// part has settled: every slide ends still, which is what lets the next
	// one fade in over a single picture of it.
	settleMargin = 0.3
	// fadeSeconds is the cross-fade from one slide to the next.
	fadeSeconds = 0.35
)

// shot is one slide on the film.
type shot struct {
	// Number is the slide's place, from 1.
	Number int
	// Start is the slide's first frame in the video; Frames how many it
	// has.
	Start, Frames int
	// Voice is its narration's length in seconds, which starts leadIn into
	// the slide.
	Voice float64
	// Starts are when each build step begins, in seconds into the slide.
	Starts deckrender.Starts
}

func (s shot) seconds() float64 { return float64(s.Frames) / fps }

// plan places every slide on the frame grid: its narration with the lead-in
// before and the pause after, each part coming in as its measured passage
// starts, and never shorter than its build takes to settle. Lengths are
// whole frames, so no rounding accumulates from slide to slide and every
// slide's voice starts on the frame it is meant to.
func plan(video Authored, voices []voiced) ([]shot, error) {
	if len(voices) != len(video.Scenes) || len(voices) != len(video.Deck.Slides) {
		return nil, fmt.Errorf("%d narrations for %d slides", len(voices), len(video.Scenes))
	}
	shots := make([]shot, len(voices))
	start := 0
	for i, voice := range voices {
		// One measured passage per build step (deckspec.Check holds a
		// video's slides to it); a mismatch here is a fault in this
		// worker, said, never papered over by spreading parts out.
		steps := deckspec.Steps(video.Deck.Slides[i])
		if len(voice.Starts) != steps {
			return nil, fmt.Errorf("slide %d has %d build steps and %d voiced passages", voice.Number, steps, len(voice.Starts))
		}
		// The slide itself is in from its first frame; each later part
		// comes in earlyBy before its passage starts, and never before
		// the part ahead of it.
		starts := make(deckrender.Starts, steps)
		for k := 1; k < steps; k++ {
			starts[k] = math.Max(starts[k-1], leadIn+voice.Starts[k]-earlyBy)
		}
		seconds := math.Max(leadIn+voice.Seconds+tailOut, video.Scenes[i].Settled(starts)+settleMargin)
		// The epsilon keeps 7.1 s from being 213.00000000000003 frames,
		// which would round up to a frame of silence nobody planned.
		frames := int(math.Ceil(seconds*fps - 1e-9))
		shots[i] = shot{Number: voice.Number, Start: start, Frames: frames, Voice: voice.Seconds, Starts: starts}
		start += frames
	}
	return shots, nil
}

// totalFrames is the film's length in frames.
func totalFrames(shots []shot) int {
	total := 0
	for _, s := range shots {
		total += s.Frames
	}
	return total
}

// picture is one frame to draw: which slide, at what moment of it, and
// whether the slide before is fading out over it.
type picture struct {
	Slide int
	// At is seconds into the slide.
	At float64
	// Fade is how much of the previous slide still shows (0 for none).
	Fade float64
}

// cut is a picture held for a run of frames.
type cut struct {
	Picture picture
	Frames  int
}

// cuts is the film as pictures to draw and how long each is held. A frame
// is drawn when something on it moves, when the previous slide is fading
// out over it, or when the slide has just settled (so the held picture is
// the settled one, not the last moving frame at ninety-eight per cent);
// otherwise the picture before it is held.
func cuts(video Authored, shots []shot) []cut {
	var out []cut
	fade := int(math.Round(fadeSeconds * fps))
	for i, s := range shots {
		spans := video.Scenes[i].Moving(s.Starts)
		moving := func(t float64) bool {
			for _, span := range spans {
				// A frame drawn at t shows the motion at t; the frame
				// whose time is past a span's end is the settled one.
				if t >= span[0] && t < span[1] {
					return true
				}
			}
			return false
		}
		held := false
		for f := 0; f < s.Frames; f++ {
			t := float64(f) / fps
			p := picture{Slide: i, At: t}
			if i > 0 && f < fade {
				p.Fade = 1 - float64(f+1)/float64(fade+1)
			}
			if f > 0 && !moving(t) && p.Fade == 0 && held {
				out[len(out)-1].Frames++
				continue
			}
			// Drawn: the first frame of a slide, a moving or fading one,
			// or the first still frame after them, which is then held.
			held = !moving(t) && p.Fade == 0
			out = append(out, cut{Picture: p, Frames: 1})
		}
	}
	return out
}
