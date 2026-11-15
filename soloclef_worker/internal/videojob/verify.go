package videojob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The checks a finished MP4 passes before it is kept
// (app/artifacts/verification/formats/video.py, the Python worker's): one
// video and one audio stream, at 1920 by 1080, the length planned, the
// narration not ending early, and no slide blank. They run here, on the
// file this worker just wrote, with ffprobe and ffmpeg.
const (
	videoWidth        = 1920
	videoHeight       = 1080
	durationTolerance = 0.5
	minFrameLevels    = 4
	minFrameStddev    = 1.0
)

// verified is what the checks found.
type verified struct {
	// Findings are what is wrong with the video. None means it passed.
	Findings []string
	// SHA256 is the digest of the file that was checked: the bytes later
	// stored must be these.
	SHA256 string
	// Seconds is the file's own length, which is what a player shows.
	Seconds float64
}

// frameSample is a moment of one slide to look at.
type frameSample struct {
	Slide int
	At    float64
}

// frameSamples are the end of each slide's narration on the film, when
// every part of it is on screen.
func frameSamples(shots []shot) []frameSample {
	out := make([]frameSample, len(shots))
	for i, s := range shots {
		at := float64(s.Start)/fps + min(leadIn+s.Voice, s.seconds()-0.1)
		out[i] = frameSample{Slide: s.Number, At: at}
	}
	return out
}

// verifyVideo checks the MP4 at output, expected seconds long.
//
// An error is a check that could not run, not the video failing one.
func verifyVideo(ctx context.Context, tools mediaTools, output string, expected float64, samples []frameSample) (verified, error) {
	probe, err := tools.run(ctx, tools.ffprobe, "-v", "error", "-count_packets", "-of", "json", "-show_entries",
		"format=duration:stream=codec_type,width,height,duration,nb_read_packets", output)
	if err != nil {
		return verified{}, fmt.Errorf("could not probe the video: %w", err)
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
	if err := json.Unmarshal(probe, &probed); err != nil {
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
	if out.Seconds > 0 && math.Abs(out.Seconds-expected) > durationTolerance {
		finding(fmt.Sprintf("Video runs %.2fs and was planned at %.2fs", out.Seconds, expected))
	}
	if out.Seconds > 0 {
		// A frame from each slide, as 64 by 36 grey levels: a slide whose
		// frame is one colour drew nothing.
		for _, sample := range samples {
			raw, err := tools.run(ctx, tools.ffmpeg, "-nostdin", "-v", "error", "-ss", strconv.FormatFloat(sample.At, 'f', 3, 64), "-i", output,
				"-frames:v", "1", "-vf", "scale=64:36,format=gray", "-f", "rawvideo", "-")
			if err != nil {
				return verified{}, fmt.Errorf("could not sample slide %d's frame: %w", sample.Slide, err)
			}
			levels, deviation := greyLevels(raw)
			if levels < minFrameLevels || deviation < minFrameStddev {
				finding(fmt.Sprintf("Slide %d's frame at %.2fs is blank or single-colour (%d grey levels, deviation %.2f)", sample.Slide, sample.At, levels, deviation))
			}
		}
	}
	file, err := os.Open(output)
	if err != nil {
		return verified{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return verified{}, err
	}
	out.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return out, nil
}

// greyLevels is how many grey levels a frame's pixels use and how far they
// spread.
func greyLevels(pixels []byte) (int, float64) {
	if len(pixels) == 0 {
		return 0, 0
	}
	seen := map[byte]bool{}
	sum := 0.0
	for _, p := range pixels {
		seen[p] = true
		sum += float64(p)
	}
	mean := sum / float64(len(pixels))
	spread := 0.0
	for _, p := range pixels {
		spread += (float64(p) - mean) * (float64(p) - mean)
	}
	return len(seen), math.Sqrt(spread / float64(len(pixels)))
}

// positive is a probe's number, or 0 when it is not a positive one.
func positive(text string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || value <= 0 || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0
	}
	return value
}

// mediaTools are the ffmpeg and ffprobe the worker image carries.
type mediaTools struct{ ffmpeg, ffprobe string }

// run runs one of them and answers its standard output.
func (m mediaTools) run(ctx context.Context, program string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(program), err, tail(stderr.String(), 500))
	}
	return out, nil
}
