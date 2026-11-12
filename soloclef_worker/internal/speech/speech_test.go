package speech

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/audiofile"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/proxystub"
)

func TestTheServiceSettingNamesTheRosterAndTheModel(t *testing.T) {
	cases := map[string]Service{
		"openai/openrouter-kokoro-82m-tts": {Roster: Kokoro, Model: "openrouter-kokoro-82m-tts"},
		"openai/gpt-4o-mini-tts":           {Roster: OpenAI, Model: "gpt-4o-mini-tts"},
		"vertex_ai/studio":                 {Roster: Vertex, Model: "studio"},
	}
	for setting, want := range cases {
		got, err := ReadService(setting)
		if err != nil || got != want {
			t.Errorf("%s: got %+v, %v; want %+v", setting, got, err, want)
		}
	}
	for _, refused := range []string{"", "tts-1", "local/kokoro", "elevenlabs/x"} {
		if _, err := ReadService(refused); err == nil {
			t.Errorf("%q was accepted", refused)
		}
	}
}

func TestAVoiceFromAnotherRosterIsRefusedByName(t *testing.T) {
	service := Service{Roster: OpenAI, Model: "gpt-4o-mini-tts"}
	if voice, err := service.Voice("openai:marin"); err != nil || voice != "marin" {
		t.Fatalf("got %v, %v", voice, err)
	}
	_, err := service.Voice("kokoro:af_heart")
	if err == nil || !strings.Contains(err.Error(), "kokoro") || !strings.Contains(err.Error(), "gpt-4o-mini-tts") {
		t.Fatalf("got %v, want a refusal naming both", err)
	}
	vertex := Service{Roster: Vertex, Model: "studio"}
	native, err := vertex.Voice("vertex_ai:en-GB-Studio-A")
	if err != nil || native.(map[string]string)["languageCode"] != "en-UK" {
		t.Fatalf("the British Studio voice is %v, %v", native, err)
	}
}

func client(t *testing.T, stub *proxystub.Server, model string) Client {
	t.Helper()
	tools, err := audiofile.Find()
	if err != nil {
		t.Skipf("ffmpeg is not installed here: %v", err)
	}
	service, err := ReadService("openai/" + model)
	if err != nil {
		t.Fatal(err)
	}
	c := Client{BaseURL: stub.BaseURL(), Key: "k", Service: service, HTTP: &http.Client{}, Audio: tools,
		AttemptTimeout: 5 * time.Second, MaxAttempts: 3, Backoff: time.Millisecond,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	return c
}

// A WAV answer and a bare-sample answer both come back as MP3.
func TestEveryAnswerBecomesMP3(t *testing.T) {
	stub := proxystub.Start()
	defer stub.Close()
	speaker := client(t, stub, "gpt-4o-mini-tts")
	for name, answer := range map[string][]byte{"wav": proxystub.Tone(0.3), "bare": proxystub.PCM(0.3)} {
		stub.Speech = func(proxystub.SpeechRequest) ([]byte, int) { return answer, http.StatusOK }
		audio, err := speaker.Speak(context.Background(), Line{Text: "Hello.", Voice: "marin", Instructions: "Warm."})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !audiofile.IsMP3(audio) {
			t.Fatalf("%s: not MP3", name)
		}
	}
	sent := stub.Speeches()[0]
	if sent.Model != "gpt-4o-mini-tts" || sent.Voice != "marin" || sent.Instructions != "Warm." || sent.ResponseFormat != "mp3" {
		t.Fatalf("the request was %+v", sent)
	}
}

// A silent answer and a 503 are retried; the third attempt's audio is kept.
func TestAFailureThatMayPassIsTriedAgain(t *testing.T) {
	stub := proxystub.Start()
	defer stub.Close()
	speaker := client(t, stub, "tts-1")
	answers := []func() ([]byte, int){
		func() ([]byte, int) { return proxystub.PCM(0.01), http.StatusOK },
		func() ([]byte, int) { return nil, http.StatusServiceUnavailable },
		func() ([]byte, int) { return proxystub.Tone(0.3), http.StatusOK },
	}
	stub.Speech = func(proxystub.SpeechRequest) ([]byte, int) {
		answer := answers[0]
		answers = answers[1:]
		return answer()
	}
	if _, err := speaker.Speak(context.Background(), Line{Text: "Hi.", Voice: "alloy", Instructions: "Warm."}); err != nil {
		t.Fatal(err)
	}
	speeches := stub.Speeches()
	if len(speeches) != 3 {
		t.Fatalf("%d attempts, want 3", len(speeches))
	}
	if speeches[0].Instructions != "" {
		t.Fatal("tts-1 was sent a speaking style it refuses")
	}
}

func TestARefusedVoiceFailsOnTheFirstAttempt(t *testing.T) {
	stub := proxystub.Start()
	defer stub.Close()
	speaker := client(t, stub, "tts-1")
	stub.Speech = func(proxystub.SpeechRequest) ([]byte, int) { return nil, http.StatusBadRequest }
	if _, err := speaker.Speak(context.Background(), Line{Text: "Hi.", Voice: "nobody"}); err == nil {
		t.Fatal("a refused voice was accepted")
	}
	if len(stub.Speeches()) != 1 {
		t.Fatalf("%d attempts, want 1", len(stub.Speeches()))
	}
}
