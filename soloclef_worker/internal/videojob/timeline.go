package videojob

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
)

// How long each scene is held, in whole frames, for a video assembled from
// stills (app/deliverables/video/stills_assembly/scene_timeline.py).
//
// Every scene's place is the sum of the ones before it, so a scene rounded
// the wrong way does not stay a local error: it slides every later slide
// against its narration and clips the last one. The arithmetic is therefore
// exact (rationals and whole frames), never floating-point seconds, and the
// rest of the assembly takes its lengths from here.

// entranceSeconds is how much of a scene is real animation: the stagger
// helper brings a scene's elements in, and once the last has settled the
// scene is a still. Measured on 2026-11-08 against a six-scene video at 30
// fps, the slowest scene settled by frame 105; four seconds carries that
// with half a second to spare.
const entranceSeconds = 4.0

func entranceFrames(fps int) int { return int(math.Ceil(entranceSeconds * float64(fps))) }

// hold is one scene on the grid: its entrance rendered as video, then its
// still held to the end of its narration.
type hold struct {
	Number   int
	Frames   int
	Entrance int
	Start    int
}

func (h hold) stillFrames() int { return h.Frames - h.Entrance }

type timeline struct {
	FPS    int
	Scenes []hold
}

func (t timeline) totalFrames() int {
	total := 0
	for _, scene := range t.Scenes {
		total += scene.Frames
	}
	return total
}

// seconds is the video's exact length.
func (t timeline) seconds() *big.Rat { return big.NewRat(int64(t.totalFrames()), int64(t.FPS)) }

// sceneFrames is the frames a narration of seconds holds a scene for: at
// least minFrames, and never a frame short of its voice. The seconds are
// read as the decimal they print as, so 2.1 is 2.1 and not the binary
// fraction below it that would round one frame down.
func sceneFrames(seconds float64, fps, minFrames int) (int, error) {
	exact, ok := new(big.Rat).SetString(strconv.FormatFloat(seconds, 'f', -1, 64))
	if !ok || exact.Sign() <= 0 {
		return 0, fmt.Errorf("a narration of %v seconds is not a length", seconds)
	}
	exact.Mul(exact, big.NewRat(int64(fps), 1))
	whole := new(big.Int).Quo(exact.Num(), exact.Denom())
	if new(big.Rat).SetInt(whole).Cmp(exact) < 0 {
		whole.Add(whole, big.NewInt(1))
	}
	return max(minFrames, int(whole.Int64())), nil
}

// buildTimeline places every narrated scene on the grid, in order.
func buildTimeline(voices []voiced, fps, minFrames, entrance int) (timeline, error) {
	if len(voices) == 0 || fps <= 0 || minFrames <= 0 || entrance <= 0 {
		return timeline{}, errors.New("a timeline needs scenes, a frame rate, a shortest scene and an entrance")
	}
	out := timeline{FPS: fps}
	start := 0
	for _, voice := range voices {
		frames, err := sceneFrames(voice.Seconds, fps, minFrames)
		if err != nil {
			return timeline{}, fmt.Errorf("scene %d: %w", voice.Number, err)
		}
		out.Scenes = append(out.Scenes, hold{Number: voice.Number, Frames: frames, Entrance: min(entrance, frames), Start: start})
		start += frames
	}
	return out, nil
}

// withRendered re-cuts the timeline around the entrances Remotion actually
// rendered. It measures each scene's audio itself, so a scene it thinks a
// frame shorter renders a shorter entrance than asked: the scene keeps its
// length and its still covers the difference. A longer one would push the
// video past its audio and is refused.
func (t timeline) withRendered(rendered map[int]int) (timeline, error) {
	out := timeline{FPS: t.FPS, Scenes: append([]hold(nil), t.Scenes...)}
	for i, scene := range out.Scenes {
		frames, found := rendered[scene.Number]
		if !found {
			return timeline{}, fmt.Errorf("scene %d was not rendered", scene.Number)
		}
		if frames <= 0 || frames > scene.Entrance {
			return timeline{}, fmt.Errorf("scene %d rendered %d entrance frames, which is not within the %d it was asked for", scene.Number, frames, scene.Entrance)
		}
		out.Scenes[i].Entrance = frames
	}
	return out, nil
}
