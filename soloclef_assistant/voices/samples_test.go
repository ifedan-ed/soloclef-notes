package voices

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mp3 is bytes that begin as an MP3 file does. No audio in this file was
// ever synthesised by anything.
var mp3 = append([]byte("ID3\x04\x00"), bytes.Repeat([]byte{0x55}, 64)...)

// booth is the speech service for one test: what it answers, and every
// sentence it was asked to speak.
type booth struct {
	mutex  sync.Mutex
	audio  []byte
	err    error
	spoken []string
	// hold, when set, keeps Speak from answering until it is closed.
	hold chan struct{}
}

func (b *booth) Speak(_ context.Context, model string, voice any, text string) ([]byte, error) {
	b.mutex.Lock()
	b.spoken = append(b.spoken, model+"|"+text)
	hold := b.hold
	b.mutex.Unlock()
	if hold != nil {
		<-hold
	}
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.audio, b.err
}

func (b *booth) calls() int {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return len(b.spoken)
}

// shelf is the sample store for one test. Two Samples given one shelf are
// two replicas of this tier.
type shelf struct {
	mutex     sync.Mutex
	kept      map[string][]byte
	readFails bool
	keepFails bool
}

func newShelf() *shelf { return &shelf{kept: map[string][]byte{}} }

func (s *shelf) Sample(_ context.Context, key string) ([]byte, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.readFails {
		return nil, false, errors.New("redis: connection refused")
	}
	audio, found := s.kept[key]
	return audio, found, nil
}

func (s *shelf) Keep(_ context.Context, key string, audio []byte) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.keepFails {
		return errors.New("redis: read only replica")
	}
	s.kept[key] = audio
	return nil
}

func kokoroSamples(t *testing.T, speaker Speaker, store SampleStore, model string) *Samples {
	t.Helper()
	catalogue, err := New(Settings{Service: "openai/" + model, APIBase: proxyAt}, neverAsked{t}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	return NewSamples(catalogue, speaker, store, quiet())
}

func voice(t *testing.T, id string) Voice {
	t.Helper()
	for _, written := range [][]Voice{kokoroVoices, openAIVoices} {
		for _, voice := range written {
			if voice.ID == id {
				return voice
			}
		}
	}
	t.Fatalf("no voice %s", id)
	return Voice{}
}

func TestAVoiceIsSynthesisedOnceForTheWholeDeployment(t *testing.T) {
	speaker, store := &booth{audio: mp3}, newShelf()
	here := kokoroSamples(t, speaker, store, "kokoro-82m")

	first, err := here.Of(context.Background(), voice(t, "kokoro:ef_dora"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Audio, mp3) || first.ContentType != "audio/mpeg" {
		t.Errorf("the sample is %d bytes of %s", len(first.Audio), first.ContentType)
	}
	// A Spanish voice says the Spanish sentence, to the deployment's model.
	if got := speaker.spoken[0]; got != "kokoro-82m|"+sampleTexts["es"] {
		t.Errorf("asked for %q", got)
	}

	// Again here, and on another replica that shares the store.
	there := kokoroSamples(t, speaker, store, "kokoro-82m")
	for _, samples := range []*Samples{here, there, here} {
		again, err := samples.Of(context.Background(), voice(t, "kokoro:ef_dora"))
		if err != nil || !bytes.Equal(again.Audio, mp3) || again.ContentType != "audio/mpeg" {
			t.Fatalf("a kept sample came back as %d bytes of %q, %v", len(again.Audio), again.ContentType, err)
		}
	}
	if speaker.calls() != 1 {
		t.Errorf("the speech service was called %d times for one voice", speaker.calls())
	}

	// Another voice is another sample; another model is too, because the
	// same voice name on a different model is a different sound.
	here.Of(context.Background(), voice(t, "kokoro:em_alex"))
	kokoroSamples(t, speaker, store, "kokoro-v2").Of(context.Background(), voice(t, "kokoro:ef_dora"))
	if speaker.calls() != 3 {
		t.Errorf("%d calls after a second voice and a second model, want 3", speaker.calls())
	}
}

func TestListenersWhoPressPlayTogetherShareOneSynthesis(t *testing.T) {
	speaker, store := &booth{audio: mp3, hold: make(chan struct{})}, newShelf()
	samples := kokoroSamples(t, speaker, store, "kokoro-82m")
	var together sync.WaitGroup
	var answered atomic.Int32
	for range 25 {
		together.Add(1)
		go func() {
			defer together.Done()
			sample, err := samples.Of(context.Background(), voice(t, "kokoro:af_heart"))
			if err == nil && bytes.Equal(sample.Audio, mp3) {
				answered.Add(1)
			}
		}()
	}
	// Let them all arrive while the one call is still out.
	for speaker.calls() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(speaker.hold)
	together.Wait()
	if speaker.calls() != 1 || answered.Load() != 25 {
		t.Errorf("%d calls for 25 listeners, %d of whom were answered", speaker.calls(), answered.Load())
	}
}

// Without the store nothing counts the calls, so none is made.
func TestNoSampleIsMadeWhenTheStoreCannotBeRead(t *testing.T) {
	speaker, store := &booth{audio: mp3}, newShelf()
	store.readFails = true
	samples := kokoroSamples(t, speaker, store, "kokoro-82m")
	for range 3 {
		_, err := samples.Of(context.Background(), voice(t, "kokoro:af_heart"))
		if !errors.Is(err, ErrStoreUnavailable) {
			t.Fatalf("with the store down the answer is %v, want ErrStoreUnavailable", err)
		}
	}
	if speaker.calls() != 0 {
		t.Errorf("the speech service was called %d times with nothing to keep the result in", speaker.calls())
	}
}

func TestAFailedSynthesisIsRememberedSoPlayCannotBeHammered(t *testing.T) {
	speaker, store := &booth{err: errors.New("503 from the provider")}, newShelf()
	samples := kokoroSamples(t, speaker, store, "kokoro-82m")
	clock := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	samples.now = func() time.Time { return clock }

	for range 4 {
		if _, err := samples.Of(context.Background(), voice(t, "kokoro:af_heart")); err == nil {
			t.Fatal("a failed synthesis was answered as a sample")
		}
	}
	if speaker.calls() != 1 {
		t.Fatalf("a failing provider was called %d times in a row", speaker.calls())
	}
	// A different voice is not held back by this one's failure.
	samples.Of(context.Background(), voice(t, "kokoro:am_adam"))
	if speaker.calls() != 2 {
		t.Errorf("another voice was not tried (%d calls)", speaker.calls())
	}

	// Later, the provider has recovered.
	speaker.mutex.Lock()
	speaker.audio, speaker.err = mp3, nil
	speaker.mutex.Unlock()
	clock = clock.Add(sampleRetryAfter + time.Second)
	if _, err := samples.Of(context.Background(), voice(t, "kokoro:af_heart")); err != nil {
		t.Errorf("after the wait the sample is still %v", err)
	}
}

func TestASampleThatCannotBeKeptIsStillPlayedAndNotMadeTwice(t *testing.T) {
	speaker, store := &booth{audio: mp3}, newShelf()
	store.keepFails = true
	samples := kokoroSamples(t, speaker, store, "kokoro-82m")
	sample, err := samples.Of(context.Background(), voice(t, "kokoro:af_heart"))
	if err != nil || !bytes.Equal(sample.Audio, mp3) {
		t.Fatalf("the sample that was paid for was not played: %v", err)
	}
	if _, err := samples.Of(context.Background(), voice(t, "kokoro:af_heart")); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("the next request is %v, want ErrStoreUnavailable", err)
	}
	if speaker.calls() != 1 {
		t.Errorf("a store that keeps nothing let %d calls through", speaker.calls())
	}
}

func TestBareSamplesAreGivenAHeaderABrowserCanPlay(t *testing.T) {
	// A quarter of a second of 16-bit silence with a click in it: what a
	// provider answers when it ignores the request for MP3.
	bare := make([]byte, 12000)
	bare[100], bare[101] = 0x10, 0x20
	speaker, store := &booth{audio: bare}, newShelf()
	sample, err := kokoroSamples(t, speaker, store, "kokoro-82m").Of(context.Background(), voice(t, "kokoro:af_heart"))
	if err != nil {
		t.Fatal(err)
	}
	if sample.ContentType != "audio/wav" || len(sample.Audio) != 44+len(bare) {
		t.Fatalf("bare samples came back as %d bytes of %s", len(sample.Audio), sample.ContentType)
	}
	header := sample.Audio[:44]
	if string(header[0:4]) != "RIFF" || string(header[8:16]) != "WAVEfmt " || string(header[36:40]) != "data" {
		t.Errorf("the header is %q", header)
	}
	if rate := binary.LittleEndian.Uint32(header[24:28]); rate != 24000 {
		t.Errorf("the sample rate is %d", rate)
	}
	if length := binary.LittleEndian.Uint32(header[40:44]); int(length) != len(bare) {
		t.Errorf("the data length is %d", length)
	}
	if !bytes.Equal(sample.Audio[44:], bare) {
		t.Error("the samples were changed")
	}
	// What is kept is the playable file, so a later listener gets the same.
	for _, kept := range store.kept {
		if !bytes.HasPrefix(kept, []byte("RIFF")) {
			t.Error("the bare samples were kept, not the file")
		}
	}
}

func TestAnAnswerThatIsNotAudioIsNotPlayedOrKept(t *testing.T) {
	for name, answer := range map[string][]byte{
		"a JSON error with a 200": []byte(`  {"error": {"message": "voice not found"}}` + strings.Repeat(" ", 6000)),
		"an HTML page":            []byte("<html>" + strings.Repeat("x", 6000)),
		"a few stray bytes":       {1, 2, 3, 4},
		"an odd number of bytes":  make([]byte, 9001),
	} {
		speaker, store := &booth{audio: answer}, newShelf()
		if _, err := kokoroSamples(t, speaker, store, "kokoro-82m").Of(context.Background(), voice(t, "kokoro:af_heart")); err == nil {
			t.Errorf("%s was answered as a sample", name)
		}
		if len(store.kept) != 0 {
			t.Errorf("%s was kept", name)
		}
	}
}

func TestAudioIsNamedByWhatItIs(t *testing.T) {
	for name, c := range map[string]struct {
		start []byte
		want  string
	}{
		"an MP3 with a tag":    {[]byte("ID3\x03"), "audio/mpeg"},
		"an MP3 with no tag":   {[]byte{0xFF, 0xFB, 0x90, 0x00}, "audio/mpeg"},
		"AAC in ADTS":          {[]byte{0xFF, 0xF1, 0x50, 0x80}, "audio/aac"},
		"a WAV file":           {[]byte("RIFF\x24\x00\x00\x00WAVEfmt "), "audio/wav"},
		"Ogg":                  {[]byte("OggS\x00\x02"), "audio/ogg"},
		"FLAC":                 {[]byte("fLaC\x00\x00"), "audio/flac"},
		"a RIFF that is video": {[]byte("RIFF\x24\x00\x00\x00AVI LIST"), ""},
		"nothing known":        {[]byte{0x00, 0x01, 0x02, 0x03}, ""},
	} {
		if got, _ := container(c.start); got != c.want {
			t.Errorf("%s is %q, want %q", name, got, c.want)
		}
	}
}

// A voice that speaks anything says the English sentence.
func TestAVoiceOfAnyLanguageIsSampledInEnglish(t *testing.T) {
	speaker, store := &booth{audio: mp3}, newShelf()
	catalogue, err := New(Settings{Service: "openai/tts-1", APIBase: proxyAt}, &listing{voices: []string{"coral"}}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSamples(catalogue, speaker, store, quiet()).Of(context.Background(), voice(t, "openai:coral")); err != nil {
		t.Fatal(err)
	}
	if got := speaker.spoken[0]; got != "tts-1|"+sampleTexts["en"] {
		t.Errorf("asked for %q", got)
	}
}

// redisVariable names a throwaway Redis for the one test of the real sample
// store. It writes and deletes its own key and nothing else, but it must
// still never name a Redis anyone uses. With it unset the test is skipped.
//
//	docker run -d --rm --name voice-sample-redis -p 127.0.0.1:39742:6379 redis:8-alpine
//	SOLOCLEF_API_TEST_VOICES_REDIS=redis://127.0.0.1:39742/0 \
//	  go test ./voices/ -run OverARealRedis -count=1 -v
//	docker rm -f voice-sample-redis
const redisVariable = "SOLOCLEF_API_TEST_VOICES_REDIS"

func TestSamplesAreKeptOverARealRedis(t *testing.T) {
	address := os.Getenv(redisVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Redis", redisVariable)
	}
	store, err := NewValkeySamples(address)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	const key = "a-test-of-the-sample-store"

	if _, found, err := store.Sample(ctx, key); err != nil || found {
		t.Fatalf("a key nobody kept: found %v, err %v", found, err)
	}
	// Audio is bytes, not text: every value a byte can hold, and a NUL.
	audio := make([]byte, 0, 512)
	for value := range 256 {
		audio = append(audio, byte(value), 0x00)
	}
	if err := store.Keep(ctx, key, audio); err != nil {
		t.Fatal(err)
	}
	kept, found, err := store.Sample(ctx, key)
	if err != nil || !found || !bytes.Equal(kept, audio) {
		t.Errorf("kept %d bytes, got back %d (found %v, err %v)", len(audio), len(kept), found, err)
	}

	// It expires, in about the lifetime it was given, under the prefix.
	client, _ := store.connection()
	seconds, err := client.Do(ctx, client.B().Ttl().Key(sampleKeyPrefix+key).Build()).AsInt64()
	if err != nil || seconds <= 0 || seconds > int64(sampleLifetime.Seconds()) {
		t.Errorf("the sample expires in %d seconds (err %v)", seconds, err)
	}
	client.Do(ctx, client.B().Del().Key(sampleKeyPrefix+key).Build())

	// And through Samples, as a second replica would find it.
	speaker := &booth{audio: mp3}
	first := kokoroSamples(t, speaker, store, "a-test-model")
	if _, err := first.Of(ctx, voice(t, "kokoro:af_heart")); err != nil {
		t.Fatal(err)
	}
	second := kokoroSamples(t, speaker, store, "a-test-model")
	sample, err := second.Of(ctx, voice(t, "kokoro:af_heart"))
	if err != nil || !bytes.Equal(sample.Audio, mp3) || speaker.calls() != 1 {
		t.Errorf("a second replica got %d bytes after %d calls (err %v)", len(sample.Audio), speaker.calls(), err)
	}
	text := sampleTexts["en"]
	sampleOf, _ := sampleKey("a-test-model", voice(t, "kokoro:af_heart"), "en-US", text)
	client.Do(ctx, client.B().Del().Key(sampleKeyPrefix+sampleOf).Build())
}
