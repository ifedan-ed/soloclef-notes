package videojob

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The checks a rendered MP4 passes before it is kept
// (app/artifacts/verification/formats/video.py). They run in the sandbox,
// where the file is: only a few lines of metadata cross back.
const (
	videoWidth        = 1920
	videoHeight       = 1080
	durationTolerance = 0.5
	minFrameLevels    = 4
	minFrameStddev    = 1.0
)

var sha256Line = regexp.MustCompile(`^[0-9a-f]{64}$`)

// verified is what the checks found.
type verified struct {
	// Findings are what is wrong with the video: each is something a repair
	// of the scenes can address. None means the video passed.
	Findings []string
	// SHA256 is the digest of the file that was checked: the bytes later
	// stored must be these.
	SHA256 string
	// Seconds is the file's own length, which is what a player shows.
	Seconds float64
}

// frameSample is a moment of one scene to look at.
type frameSample struct {
	Scene int
	At    float64
}

// frameSamples are the middle of each scene, on the grid the render puts
// the scenes on: by then every element is on screen (the layout rules bring
// them in within two seconds) and none has gone.
func frameSamples(voices []voiced) []frameSample {
	var out []frameSample
	start := 0
	for _, voice := range voices {
		frames, err := sceneFrames(voice.Seconds, fps, minFrames)
		if err != nil {
			continue
		}
		out = append(out, frameSample{Scene: voice.Number, At: float64(start+frames/2) / fps})
		start += frames
	}
	return out
}

// verifyVideo checks the MP4 at output. expected is the length it was
// planned at, or 0 to read it from the sidecar a frames render writes.
//
// An error is the sandbox failing to run a check, not the video failing
// one: the Python worker turned those into findings and handed them to a
// repair, which spent a model call asking the scenes to fix an ffprobe that
// did not answer.
func verifyVideo(ctx context.Context, box Sandbox, output string, expected float64, samples []frameSample, limit time.Duration) (verified, error) {
	run := func(command, doing string) (string, error) {
		result, err := box.RunWithin(ctx, command, limit)
		if err != nil {
			return "", fmt.Errorf("could not %s: %w", doing, err)
		}
		if !result.OK() {
			return "", fmt.Errorf("could not %s: %s", doing, tail(result.Output, 2000))
		}
		return strings.TrimSpace(result.Output), nil
	}
	quoted := quote(output)
	probe, err := run("ffprobe -v error -count_packets -of json -show_entries "+
		"format=duration:stream=codec_type,width,height,duration,nb_read_packets "+quoted, "probe the video")
	if err != nil {
		return verified{}, err
	}
	var probed struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type     string `json:"codec_type"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Duration string `json:"duration"`
			Packets  string `json:"nb_read_packets"`
		} `json:"streams"`
	}
	if err := json.Unmarshal([]byte(probe), &probed); err != nil {
		return verified{}, fmt.Errorf("the video's probe is not readable: %w", err)
	}
	var out verified
	finding := func(text string) { out.Findings = append(out.Findings, text) }
	out.Seconds = positive(probed.Format.Duration)
	if out.Seconds == 0 {
		finding("Video duration must be greater than zero")
	}
	var videos, audios int
	for _, stream := range probed.Streams {
		switch stream.Type {
		case "video":
			videos++
			if stream.Width != videoWidth || stream.Height != videoHeight {
				finding(fmt.Sprintf("Video resolution must be %dx%d", videoWidth, videoHeight))
			}
		case "audio":
			audios++
			if packets, _ := strconv.Atoi(stream.Packets); packets <= 0 {
				finding("Video narration audio is empty")
			}
			if audio := positive(stream.Duration); out.Seconds > 0 && audio > 0 && out.Seconds-audio > durationTolerance {
				finding("Video narration ends before the video")
			}
		}
	}
	if videos != 1 {
		finding("Video must contain exactly one video stream")
	}
	if audios != 1 {
		finding("Video must contain exactly one narration audio stream")
	}

	if expected == 0 {
		// A frames render writes how long it meant the file to be.
		sidecar, err := box.ReadFile(ctx, output+".segments.json", 1<<20)
		if err != nil {
			return verified{}, fmt.Errorf("could not read the render's segment record: %w", err)
		}
		var segments struct {
			Expected float64 `json:"expected_duration_seconds"`
		}
		if json.Unmarshal(sidecar, &segments) != nil || segments.Expected <= 0 || math.IsInf(segments.Expected, 0) {
			return verified{}, fmt.Errorf("the render's segment record is not readable: %q", tail(string(sidecar), 300))
		}
		expected = segments.Expected
	}
	if out.Seconds > 0 && math.Abs(out.Seconds-expected) > durationTolerance {
		finding("Video duration does not match its rendered segments")
	}

	if out.Seconds > 0 {
		// A frame from the middle of each scene, as 64x36 grey levels: a
		// scene whose frame is one colour drew nothing. The Python check
		// sampled one frame, the middle of the whole video, which can land
		// on a scene's first moments while its elements are still coming
		// in and fail a good video as blank; and it could not say which
		// scene was wrong, which is what a repair needs to know.
		var times []string
		for _, sample := range samples {
			times = append(times, strconv.FormatFloat(sample.At, 'f', 3, 64))
		}
		stats, err := run("for at in "+strings.Join(times, " ")+"; do ffmpeg -nostdin -v error -ss \"$at\" -i "+quoted+
			" -frames:v 1 -vf 'scale=64:36,format=gray' -f rawvideo - | "+
			`python3 -c 'import json,statistics,sys; d=sys.stdin.buffer.read(); print(json.dumps({"levels":len(set(d)),"stddev":statistics.pstdev(d) if d else 0}))'`+
			" || exit 1; done", "sample the scenes' frames")
		if err != nil {
			return verified{}, err
		}
		lines := strings.Split(stats, "\n")
		if len(lines) != len(samples) {
			return verified{}, fmt.Errorf("the frame samples are not readable: %q", tail(stats, 300))
		}
		for i, line := range lines {
			var frame struct {
				Levels int     `json:"levels"`
				Stddev float64 `json:"stddev"`
			}
			if err := json.Unmarshal([]byte(line), &frame); err != nil {
				return verified{}, fmt.Errorf("the frame sample is not readable: %w", err)
			}
			if frame.Levels < minFrameLevels || frame.Stddev < minFrameStddev {
				finding(fmt.Sprintf("Scene %d's frame at %.2fs is blank or single-color (%d grey levels, deviation %.2f)",
					samples[i].Scene, samples[i].At, frame.Levels, frame.Stddev))
			}
		}
	}

	sum, err := run("sha256sum -- "+quoted, "hash the video")
	if err != nil {
		return verified{}, err
	}
	out.SHA256, _, _ = strings.Cut(sum, " ")
	if !sha256Line.MatchString(out.SHA256) {
		return verified{}, fmt.Errorf("the video's hash is not readable: %q", tail(sum, 200))
	}
	return out, nil
}

// positive is a probe's number, or 0 when it is not a positive one.
func positive(text string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || value <= 0 || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0
	}
	return value
}
