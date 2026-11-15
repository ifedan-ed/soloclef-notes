package audiofile

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/proxystub"
)

// speechLike is a part shaped as a voiced passage is: lead silence, then
// sound, then a tail of silence.
func speechLike(lead, sound, tail float64) []byte {
	silence := func(seconds float64) []byte { return make([]byte, 2*int(seconds*pcmRate)) }
	return bytes.Join([][]byte{silence(lead), proxystub.PCM(sound), silence(tail)}, nil)
}

// onsets is where sound begins after at least a tenth of a second of
// silence, in seconds, in decoded mono samples.
func onsets(samples []byte) []float64 {
	var out []float64
	quiet := pcmRate // the start counts as after silence
	for i := 0; i+1 < len(samples); i += 2 {
		value := int16(binary.LittleEndian.Uint16(samples[i:]))
		if value > 1000 || value < -1000 {
			if quiet >= pcmRate/10 {
				out = append(out, float64(i/2)/pcmRate)
			}
			quiet = 0
		} else if value < 100 && value > -100 {
			quiet++
		}
	}
	return out
}

// A slide's passages joined with a pause each start where their measured
// lengths and the pauses before them say: the sums a video cues its
// builds by, with no drift from the first passage to the last.
func TestPartsJoinedWithGapsStartWhereTheirLengthsSay(t *testing.T) {
	audio := tools(t)
	ctx := context.Background()
	const lead, gap = 0.1, 0.15
	var parts [][]byte
	var lengths []float64
	for _, sound := range []float64{1.0, 2.3, 0.7, 1.6, 3.1} {
		mp3, err := audio.ToMP3(ctx, speechLike(lead, sound, 0.35))
		if err != nil {
			t.Fatal(err)
		}
		seconds, _ := audio.Seconds(ctx, mp3)
		parts = append(parts, mp3)
		lengths = append(lengths, seconds)
	}
	joined, err := audio.JoinSpaced(ctx, parts, gap)
	if err != nil {
		t.Fatal(err)
	}
	if !IsMP3(joined) {
		t.Fatal("the joined parts are not MP3")
	}
	// Decoded as the video's join decodes it: by ffmpeg, from the file.
	decoded, _, err := audio.run(ctx, audio.FFmpeg, joined, "-hide_banner", "-loglevel", "error", "-i", "pipe:0",
		"-f", "s16le", "-ar", strconv.Itoa(pcmRate), "-ac", "1", "pipe:1")
	if err != nil {
		t.Fatal(err)
	}
	found := onsets(decoded)
	if len(found) != len(parts) {
		t.Fatalf("%d onsets %v for %d parts", len(found), found, len(parts))
	}
	// Each part's own encoder lead-in is part of its measured length and
	// sits before its sound; it is the same for every part, so it is
	// measured on the first and must not grow from part to part.
	start, offset := 0.0, found[0]-lead
	for i, at := range found {
		want := start + lead + offset
		if math.Abs(at-want) > 0.003 {
			t.Errorf("part %d sounds at %.4fs; its lengths and gaps put it at %.4fs", i+1, at, want)
		}
		start += lengths[i] + gap
	}
	if offset < 0 || offset > 0.08 {
		t.Errorf("the first part sounds %.4fs after its lead silence", offset)
	}
	// The joined narration is the parts and the gaps, give or take the
	// encoder's frames.
	total := start - gap
	if seconds := float64(len(decoded)) / bytesPerSecond; math.Abs(seconds-total) > 0.08 {
		t.Errorf("the joined narration decodes to %.3fs; its parts and gaps are %.3fs", seconds, total)
	}

	if one, err := audio.JoinSpaced(ctx, parts[:1], gap); err != nil || !bytes.Equal(one, parts[0]) {
		t.Errorf("one part came back changed (%v)", err)
	}
	if _, err := audio.JoinSpaced(ctx, nil, gap); err == nil {
		t.Error("nothing was joined into something")
	}
	if _, err := audio.JoinSpaced(ctx, parts, -1); err == nil {
		t.Error("a negative gap was accepted")
	}
}

// Every part is read as MP3 from a plain file and nothing else: the
// format and the protocol whitelist come before each part's -i, where
// ffmpeg takes them as that input's options, so no part's bytes can pick
// another demuxer (one that opens the files or URLs they name).
func TestEachPartIsReadAsMP3FromAFileOnly(t *testing.T) {
	paths := []string{"/tmp/x/part-000.mp3", "/tmp/x/part-001.mp3", "/tmp/x/part-002.mp3"}
	arguments := joinSpacedArguments(paths, 0.15, "/tmp/x/joined.mp3")
	inputs := 0
	for i, argument := range arguments {
		if argument != "-i" {
			continue
		}
		if i < 4 || !slices.Equal(arguments[i-4:i], []string{"-f", "mp3", "-protocol_whitelist", "file"}) {
			t.Errorf("input %q is not read as MP3 from a file only: %q", arguments[i+1], arguments[:i])
		}
		if arguments[i+1] != paths[inputs] {
			t.Errorf("input %d is %q, want %q", inputs, arguments[i+1], paths[inputs])
		}
		inputs++
	}
	if inputs != len(paths) {
		t.Errorf("%d inputs for %d parts: %q", inputs, len(paths), arguments)
	}
	if arguments[len(arguments)-1] != "/tmp/x/joined.mp3" {
		t.Errorf("the output is not last: %q", arguments)
	}
}

// A part that is not MP3 is refused before ffmpeg is started: every voiced
// part is one (ToMP3), so anything else is not what the join was given to
// read.
func TestAPartThatIsNotMP3IsRefused(t *testing.T) {
	playlist := []byte("#EXTM3U\n#EXTINF:1,\nfile:///etc/passwd\n")
	var none Tools // no binaries: a refusal must not reach them
	for _, parts := range [][][]byte{{playlist}, {proxystub.PCM(0.2), playlist}} {
		if _, err := none.JoinSpaced(context.Background(), parts, 0.1); err == nil || !strings.Contains(err.Error(), "not MP3") {
			t.Errorf("%d parts with one not MP3: %v", len(parts), err)
		}
	}
}
