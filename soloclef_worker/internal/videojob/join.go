package videojob

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// join makes the MP4 at out from the drawn pictures, each held its frames,
// and each slide's narration: H.264 at 30 frames a second and AAC, the
// shapes every browser and phone plays.
//
// It runs in two steps. First each slide is encoded on its own, several at
// once (workers): its pictures go in through ffmpeg's concat list with a
// duration each, so a held picture is decoded and converted once and then
// repeated, not written and read back once per frame. Decoding the
// pictures is most of an encode's time and one ffmpeg decodes them one
// after another, which is why the slides are encoded side by side
// (measured on a 50-second video: 15 s as one encode on a busy host).
// Then the slides' video is joined without encoding it again, and the
// narration laid under it: each slide's voice delayed by the lead-in and
// padded to its slide's exact length, so the voice starts on the frame its
// slide was planned for however long the slides before it ran.
func join(ctx context.Context, ffmpeg, dir string, workers int, film []cut, pictures []string, shots []shot, voices []voiced, out string) error {
	segments := make([]string, len(shots))
	var jobs []func(context.Context) error
	first := 0
	for i := range shots {
		// The cuts of slide i are the run that follows the previous
		// slide's.
		last := first
		for last < len(film) && film[last].Picture.Slide == i {
			last++
		}
		segments[i] = filepath.Join(dir, fmt.Sprintf("slide-%03d.mp4", i+1))
		cutsOf, picturesOf, frames, segment := film[first:last], pictures[first:last], shots[i].Frames, segments[i]
		listPath := filepath.Join(dir, fmt.Sprintf("slide-%03d.ffconcat", i+1))
		jobs = append(jobs, func(ctx context.Context) error {
			return encodeSlide(ctx, ffmpeg, dir, listPath, cutsOf, picturesOf, frames, segment)
		})
		first = last
	}
	if err := runAll(ctx, workers, jobs); err != nil {
		return err
	}

	var list strings.Builder
	list.WriteString("ffconcat version 1.0\n")
	for _, segment := range segments {
		fmt.Fprintf(&list, "file '%s'\n", filepath.Base(segment))
	}
	listPath := filepath.Join(dir, "slides.ffconcat")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o600); err != nil {
		return err
	}
	args := []string{"-nostdin", "-v", "error", "-y", "-f", "concat", "-safe", "0", "-i", listPath}
	var graph strings.Builder
	for i, voice := range voices {
		audio := filepath.Join(dir, fmt.Sprintf("voice-%03d.mp3", i+1))
		if err := os.WriteFile(audio, voice.MP3, 0o600); err != nil {
			return err
		}
		args = append(args, "-i", audio)
		seconds := shots[i].seconds()
		fmt.Fprintf(&graph, "[%d:a]aresample=48000,aformat=channel_layouts=mono,adelay=%d:all=1,apad=whole_dur=%.6f,atrim=0:%.6f[a%d];",
			i+1, int(leadIn*1000), seconds, seconds, i)
	}
	for i := range voices {
		fmt.Fprintf(&graph, "[a%d]", i)
	}
	fmt.Fprintf(&graph, "concat=n=%d:v=0:a=1[a]", len(voices))
	total := float64(totalFrames(shots)) / fps
	args = append(args, "-filter_complex", graph.String(), "-map", "0:v", "-map", "[a]",
		"-c:v", "copy", "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart",
		"-t", strconv.FormatFloat(total, 'f', 6, 64), out)
	return runFFmpeg(ctx, ffmpeg, dir, args, "join the slides and the narration")
}

// encodeSlide encodes one slide's pictures, each held its frames, to an
// MP4 of exactly frames frames and no sound.
func encodeSlide(ctx context.Context, ffmpeg, dir, listPath string, film []cut, pictures []string, frames int, out string) error {
	var list strings.Builder
	list.WriteString("ffconcat version 1.0\n")
	for i, c := range film {
		fmt.Fprintf(&list, "file '%s'\nduration %.6f\n", filepath.Base(pictures[i]), float64(c.Frames)/fps)
	}
	// The concat demuxer drops the last entry's duration unless a file
	// follows it; the last picture again ends the list.
	fmt.Fprintf(&list, "file '%s'\n", filepath.Base(pictures[len(pictures)-1]))
	if err := os.WriteFile(listPath, []byte(list.String()), 0o600); err != nil {
		return err
	}
	return runFFmpeg(ctx, ffmpeg, dir, []string{"-nostdin", "-v", "error", "-y", "-f", "concat", "-safe", "0", "-i", listPath,
		// Converted to the encoder's colours before the frame rate repeats
		// a held picture, so each picture is converted once.
		"-vf", "format=yuv420p,fps=30",
		// veryfast: the pictures are flat colour and type, which x264
		// compresses well at any preset; a slower one buys a few per cent
		// of size for twice the time. Two threads each, since several
		// slides are encoded at once.
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-g", "60", "-threads", "2",
		"-frames:v", strconv.Itoa(frames), "-an", out}, "encode a slide")
}

func runFFmpeg(ctx context.Context, ffmpeg, dir string, args []string, doing string) error {
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg could not %s: %w: %s", doing, err, tail(stderr.String(), 1500))
	}
	return nil
}
