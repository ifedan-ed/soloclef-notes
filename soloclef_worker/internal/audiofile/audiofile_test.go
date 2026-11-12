package audiofile

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/proxystub"
)

func tools(t *testing.T) Tools {
	t.Helper()
	found, err := Find()
	if err != nil {
		t.Skipf("ffmpeg is not installed here: %v", err)
	}
	return found
}

func TestContainersAreToldFromBareSamples(t *testing.T) {
	if !HasContainer(proxystub.Tone(0.1)) || IsMP3(proxystub.Tone(0.1)) {
		t.Fatal("a WAV file was not recognised as one")
	}
	if HasContainer(proxystub.PCM(0.1)) || IsMP3(proxystub.PCM(0.0001)) {
		t.Fatal("bare samples were taken for a container")
	}
	if !IsMP3([]byte("ID3\x04rest")) {
		t.Fatal("an ID3 tag was not taken for MP3")
	}
}

func TestBareSamplesAreMeasuredWithoutADecoder(t *testing.T) {
	seconds, err := Tools{}.Seconds(context.Background(), proxystub.PCM(0.5))
	if err != nil || math.Abs(seconds-0.5) > 0.001 {
		t.Fatalf("got %v, %v; want 0.5s", seconds, err)
	}
}

// The whole path a provider's answer takes: measured, made MP3, joined
// with another, and the joined file's length read back.
func TestSegmentsBecomeOneMP3OfTheirCombinedLength(t *testing.T) {
	audio := tools(t)
	ctx := context.Background()
	directory := t.TempDir()

	wav := proxystub.Tone(1)
	seconds, err := audio.Seconds(ctx, wav)
	if err != nil || math.Abs(seconds-1) > 0.01 {
		t.Fatalf("a one-second WAV measured %v (%v)", seconds, err)
	}
	var paths []string
	for index, source := range [][]byte{wav, proxystub.PCM(0.5)} {
		mp3, err := audio.ToMP3(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		if !IsMP3(mp3) {
			t.Fatalf("segment %d is not MP3 after conversion", index)
		}
		path := filepath.Join(directory, "it's segment "+string(rune('a'+index))+".mp3")
		if err := os.WriteFile(path, mp3, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	joined := filepath.Join(directory, "episode.mp3")
	if err := audio.JoinMP3(ctx, paths, joined); err != nil {
		t.Fatal(err)
	}
	length, err := audio.Duration(ctx, joined)
	if err != nil {
		t.Fatal(err)
	}
	// MP3 frames pad each segment by a few tens of milliseconds.
	if length < 1.4 || length > 1.7 {
		t.Fatalf("the joined episode is %.3fs, want about 1.5s", length)
	}
	if _, err := os.Stat(joined + ".concat.txt"); !os.IsNotExist(err) {
		t.Fatal("the segment list was left behind")
	}
}

func TestBytesThatAreNotAudioDecodeToNothing(t *testing.T) {
	audio := tools(t)
	seconds, err := audio.Seconds(context.Background(), []byte("RIFF....WAVEnot really"))
	if err != nil || seconds != 0 {
		t.Fatalf("got %v, %v; want 0 and no error", seconds, err)
	}
}

// Counting frames in Go agrees with ffprobe, for the episode format and
// for the MPEG-1 rates a provider may answer in, and a transfer cut short
// measures short.
func TestFramesAreCountedAsFFprobeReadsThem(t *testing.T) {
	audio := tools(t)
	ctx := context.Background()
	directory := t.TempDir()
	for _, rate := range []string{"24000", "44100"} {
		path := filepath.Join(directory, rate+".mp3")
		if _, _, err := audio.run(ctx, audio.FFmpeg, proxystub.Tone(3), "-hide_banner", "-loglevel", "error", "-i", "pipe:0",
			"-ar", rate, "-ac", "1", "-b:a", "128k", "-id3v2_version", "3", "-y", path); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		probed, err := audio.Duration(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		counted := MP3Seconds(data)
		if math.Abs(counted-probed) > 0.06 {
			t.Errorf("%s Hz: counted %.3fs, ffprobe %.3fs", rate, counted, probed)
		}
		if half := MP3Seconds(data[:len(data)/2]); half > counted*0.6 || half < counted*0.4 {
			t.Errorf("%s Hz: half the bytes measured %.3fs of %.3fs", rate, half, counted)
		}
	}
	if MP3Seconds([]byte("ID3 not really audio")) != 0 {
		t.Error("bytes with no frames measured some sound")
	}
}
