// Package audiofile is what the worker does to audio bytes, all of it with
// the ffmpeg and ffprobe binaries the worker image ships: recognise a
// container, measure how much sound bytes hold, turn anything into MP3, join
// MP3 segments into one file, and read a file's duration.
//
// It is the binaries and not a Go decoder because a provider behind the
// proxy may answer in any container (MP3, WAV, Ogg, AAC) or none at all,
// and ffmpeg reads every one of them the same way. Each call is one short
// process; the cost was measured in the podcast benchmark.
package audiofile

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Tools names the two binaries. Both are looked up once, at start-up, so a
// worker image without them stops there instead of failing its first
// podcast after the transcript has been paid for.
type Tools struct {
	FFmpeg  string
	FFprobe string
}

// Find locates both on PATH.
func Find() (Tools, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return Tools{}, fmt.Errorf("audiofile: ffmpeg is not on PATH: %w", err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return Tools{}, fmt.Errorf("audiofile: ffprobe is not on PATH: %w", err)
	}
	return Tools{FFmpeg: ffmpeg, FFprobe: ffprobe}, nil
}

// pcmRate is the rate bare samples arrive at (Grok Voice through
// OpenRouter answers an MP3 request with 16-bit mono 24 kHz samples and no
// header), and the rate sound is decoded at to be measured.
const pcmRate = 24000

const bytesPerSecond = pcmRate * 2

// bareSampleArguments is how ffmpeg is told what headerless bytes are.
var bareSampleArguments = []string{"-f", "s16le", "-ar", strconv.Itoa(pcmRate), "-ac", "1"}

// IsMP3 reports an ID3 tag or an MPEG audio frame at the start.
func IsMP3(data []byte) bool {
	return bytes.HasPrefix(data, []byte("ID3")) || (len(data) > 1 && data[0] == 0xFF && data[1]&0xE0 == 0xE0)
}

// HasContainer reports a container ffmpeg identifies on its own: WAV, Ogg,
// FLAC, WebM, MP4/AAC or an ADTS stream. Anything else is read as bare
// samples.
func HasContainer(data []byte) bool {
	head := data
	if len(head) > 12 {
		head = head[:12]
	}
	switch {
	case bytes.HasPrefix(head, []byte("RIFF")), bytes.HasPrefix(head, []byte("OggS")),
		bytes.HasPrefix(head, []byte("fLaC")), bytes.HasPrefix(head, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return true
	case len(head) >= 8 && bytes.Equal(head[4:8], []byte("ftyp")):
		return true
	case len(head) > 1 && head[0] == 0xFF && head[1]&0xF6 == 0xF0:
		return true
	}
	return false
}

// Seconds is how much sound the bytes hold. Bare samples are measured by
// arithmetic, as they arrived: re-encoding first would hide a few bytes of
// an error page read as samples, because an encoder pads its last frame.
// MP3 is counted from its frames (MP3Seconds), with no process started.
// Anything else is decoded by ffmpeg, and bytes that do not decode hold no
// sound.
func (t Tools) Seconds(ctx context.Context, data []byte) (float64, error) {
	if !IsMP3(data) && !HasContainer(data) {
		return float64(len(data)) / bytesPerSecond, nil
	}
	if IsMP3(data) {
		return MP3Seconds(data), nil
	}
	decoded, _, err := t.run(ctx, t.FFmpeg, data, "-hide_banner", "-loglevel", "error", "-i", "pipe:0",
		"-f", "s16le", "-ar", strconv.Itoa(pcmRate), "-ac", "1", "pipe:1")
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, nil
	}
	return float64(len(decoded)) / bytesPerSecond, nil
}

// Episode format: every line is made into the same MP3 before it is
// cached, mono at the rate speech models speak at, so that the episode can
// be joined by copying frames instead of encoding the whole of it again.
// Each line costs one short encode, done while the transcript is still
// being drafted; the join then takes a fraction of a second for an hour of
// audio (it was an encode of the whole episode, measured at most of a
// ten-minute render).
var episodeFormat = []string{"-ar", strconv.Itoa(pcmRate), "-ac", "1", "-b:a", "128k", "-f", "mp3"}

// ToMP3 returns the bytes as MP3 in the episode format, whatever they
// arrived as: bare samples, another container, or an MP3 at another rate.
func (t Tools) ToMP3(ctx context.Context, data []byte) ([]byte, error) {
	arguments := []string{"-hide_banner", "-loglevel", "error"}
	if !IsMP3(data) && !HasContainer(data) {
		arguments = append(arguments, bareSampleArguments...)
	}
	arguments = append(arguments, "-i", "pipe:0")
	arguments = append(arguments, episodeFormat...)
	arguments = append(arguments, "pipe:1")
	out, stderr, err := t.run(ctx, t.FFmpeg, data, arguments...)
	if err != nil || len(out) == 0 {
		return nil, fmt.Errorf("audiofile: the audio could not be converted to MP3: %v %s", err, clip(stderr))
	}
	return out, nil
}

// JoinMP3 writes the segments, in order, into one MP3 at output. Every
// segment must be in the episode format (ToMP3), because the frames are
// copied, not encoded again.
//
// It uses ffmpeg's concat demuxer (a list file), not a filter graph: the
// demuxer takes one input however many segments there are, so an
// hour-long episode with thousands of turns never meets a command-line
// limit.
func (t Tools) JoinMP3(ctx context.Context, segments []string, output string) error {
	if len(segments) == 0 {
		return fmt.Errorf("audiofile: no segments to join")
	}
	var list strings.Builder
	for _, path := range segments {
		// The demuxer's quoting: a single quote is closed, escaped and
		// reopened.
		fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(path, "'", `'\''`))
	}
	listFile := output + ".concat.txt"
	if err := os.WriteFile(listFile, []byte(list.String()), 0o600); err != nil {
		return fmt.Errorf("audiofile: writing the segment list: %w", err)
	}
	defer os.Remove(listFile)
	_, stderr, err := t.run(ctx, t.FFmpeg, nil, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "concat", "-safe", "0", "-i", listFile, "-c", "copy", output)
	if err != nil {
		return fmt.Errorf("audiofile: joining %d segments: %v %s", len(segments), err, clip(stderr))
	}
	return nil
}

// Duration is a file's length in seconds, as its container states it.
func (t Tools) Duration(ctx context.Context, path string) (float64, error) {
	out, stderr, err := t.run(ctx, t.FFprobe, nil, "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path)
	if err != nil {
		return 0, fmt.Errorf("audiofile: reading the duration of %s: %v %s", path, err, clip(stderr))
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("audiofile: ffprobe gave %q as a duration", strings.TrimSpace(string(out)))
	}
	return seconds, nil
}

func (t Tools) run(ctx context.Context, binary string, stdin []byte, arguments ...string) (stdout, stderr []byte, err error) {
	command := exec.CommandContext(ctx, binary, arguments...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	command.Stdout, command.Stderr = &out, &errOut
	err = command.Run()
	return out.Bytes(), errOut.Bytes(), err
}

func clip(stderr []byte) string {
	text := strings.TrimSpace(string(stderr))
	if len(text) > 300 {
		text = text[:300]
	}
	return text
}
