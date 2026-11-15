package audiofile

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// JoinSpaced joins parts, each an MP3 (as ToMP3 makes every voiced
// passage), in order, into one MP3 in the episode format, with gap seconds
// of silence between each part and the next and none before the first or
// after the last. A video uses it to make one slide's
// narration from passages voiced one by one: each passage then starts at a
// time the caller knows from the parts' own lengths (MP3Seconds of each,
// plus the gaps before it), which no single recording of the whole slide
// can say.
//
// The parts are decoded and encoded again rather than their frames copied
// (JoinMP3): the silence between them has to be an exact length, not a
// whole number of MP3 frames, and a part's decoded samples are exactly the
// frames MP3Seconds counts, so the starts the caller works out are where
// each part's sound really begins. The output is written to a file, not a
// pipe, because only a file gets the encoder's gapless header, which tells
// a decoder to drop the encoder's own lead-in: the joined narration then
// starts on its first part's first sample, as the starts assume. One part
// is returned as it is: there is nothing to join, and no encode to pay.
//
// One ffmpeg process for the whole slide, a fraction of a second for a
// minute of narration.
func (t Tools) JoinSpaced(ctx context.Context, parts [][]byte, gap float64) ([]byte, error) {
	switch {
	case len(parts) == 0:
		return nil, fmt.Errorf("audiofile: no parts to join")
	case gap < 0 || math.IsNaN(gap) || math.IsInf(gap, 0):
		return nil, fmt.Errorf("audiofile: a gap of %v seconds between parts", gap)
	}
	for i, part := range parts {
		if !IsMP3(part) {
			return nil, fmt.Errorf("audiofile: part %d is not MP3 (ToMP3 makes every voiced part one)", i+1)
		}
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	dir, err := os.MkdirTemp("", "soloclef-spaced-*")
	if err != nil {
		return nil, fmt.Errorf("audiofile: a directory to join in: %w", err)
	}
	defer os.RemoveAll(dir)

	paths := make([]string, len(parts))
	for i, part := range parts {
		paths[i] = filepath.Join(dir, fmt.Sprintf("part-%03d.mp3", i))
		if err := os.WriteFile(paths[i], part, 0o600); err != nil {
			return nil, fmt.Errorf("audiofile: writing part %d: %w", i+1, err)
		}
	}
	output := filepath.Join(dir, "joined.mp3")
	arguments := joinSpacedArguments(paths, gap, output)
	if _, stderr, err := t.run(ctx, t.FFmpeg, nil, arguments...); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("audiofile: joining %d parts: %v %s", len(parts), err, clip(stderr))
	}
	joined, err := os.ReadFile(output)
	if err != nil {
		return nil, fmt.Errorf("audiofile: reading the joined parts: %w", err)
	}
	return joined, nil
}

// spacedPartInput is how ffmpeg is told to read one part: as MP3 and from
// a local file, nothing else. A part's bytes are the caller's, made from
// what a speech provider answered, so they are not trusted to say what they
// are: left to probe, ffmpeg picks the demuxer the bytes look most like, and
// some demuxers (an HLS playlist, a concat list) open further files or
// URLs that the bytes name. Naming the format turns probing off, and the
// whitelist refuses every protocol but the plain file the part was written
// to, whatever a demuxer asks for.
var spacedPartInput = []string{"-f", "mp3", "-protocol_whitelist", "file"}

// joinSpacedArguments is the ffmpeg command line that joins the parts at
// paths, in order, with gap seconds of silence between each and the next,
// into output in the episode format.
func joinSpacedArguments(paths []string, gap float64, output string) []string {
	arguments := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	var graph strings.Builder
	for i, path := range paths {
		arguments = append(arguments, spacedPartInput...)
		arguments = append(arguments, "-i", path)
		// Every part in the one format the concat filter needs; the gap is
		// silence padded onto the end of each part but the last, so it is
		// exactly gap seconds whatever the parts' frame sizes.
		fmt.Fprintf(&graph, "[%d:a]aresample=%d,aformat=sample_fmts=s16:channel_layouts=mono", i, pcmRate)
		if i < len(paths)-1 && gap > 0 {
			fmt.Fprintf(&graph, ",apad=pad_dur=%.6f", gap)
		}
		fmt.Fprintf(&graph, "[p%d];", i)
	}
	for i := range paths {
		fmt.Fprintf(&graph, "[p%d]", i)
	}
	fmt.Fprintf(&graph, "concat=n=%d:v=0:a=1[out]", len(paths))
	arguments = append(arguments, "-filter_complex", graph.String(), "-map", "[out]")
	arguments = append(arguments, episodeFormat...)
	return append(arguments, output)
}
