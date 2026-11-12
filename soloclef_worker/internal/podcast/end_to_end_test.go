package podcast

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/audiofile"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/llm"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/proxystub"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

// The whole job against a real database, a real file store, real ffmpeg
// and a proxy that speaks the OpenAI shapes: an approved brief goes in, and
// a playable MP3 comes out as the podcast's artifact.
func TestAPodcastEndToEnd(t *testing.T) {
	tools, err := audiofile.Find()
	if err != nil {
		t.Skipf("ffmpeg is not installed here: %v", err)
	}
	f := newStoreFixture(t, statusDrafting)
	ctx := context.Background()
	// The account chose its own chat model; the podcast is drafted by it.
	testdatabase.Exec(t, f.pool, `INSERT INTO user_models (user_id, purpose, model) VALUES ($1::uuid, 'chat', 'account-model')`, f.userID)

	stub := proxystub.Start()
	defer stub.Close()
	stub.Chat = func(request proxystub.ChatRequest) (string, int) {
		if strings.Contains(request.System(), "showrunner") {
			return `{"segments":[{"title":"Opening","talking_points":["what sepsis is"],"target_words":60},` +
				`{"title":"Close","talking_points":["what to watch for"],"target_words":60}]}`, http.StatusOK
		}
		beat := "opening"
		if strings.Contains(request.System(), "segment 2 of 2") {
			beat = "closing"
		}
		return fmt.Sprintf(`Here you go: {"turns":[{"speaker":0,"text":"Welcome to the %s beat."},`+
			`{"speaker":1,"text":"Glad to be here for the %s."}]}`, beat, beat), http.StatusOK
	}
	stub.Speech = func(proxystub.SpeechRequest) ([]byte, int) { return proxystub.Tone(0.6), http.StatusOK }

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := llm.Proxy{BaseURL: stub.BaseURL(), Key: "k", HTTP: &http.Client{}, CallTimeout: 10 * time.Second,
		Retry: model.RetryPolicy{Attempts: 2, Backoff: time.Millisecond, MaxBackoff: time.Millisecond}}
	service, err := speech.ReadService("openai/gpt-4o-mini-tts")
	if err != nil {
		t.Fatal(err)
	}
	voicer := speech.Client{BaseURL: stub.BaseURL(), Key: "k", Service: service, HTTP: &http.Client{}, Audio: tools,
		AttemptTimeout: 10 * time.Second, MaxAttempts: 2, Backoff: time.Millisecond, Log: log}
	indexer := &recordingIndexer{}
	job := &Job{
		Rows:   f.store,
		Models: llm.Writers{Choices: llm.Choices{Pool: f.pool, Default: "deployment-model"}, Proxy: proxy},
		Voicer: voicer, Audio: tools, Indexer: indexer,
		Config: Config{CallTimeout: 10 * time.Second, Speech: service, Instructions: "Warm and clear.",
			Concurrency: 8, WorkDirectory: t.TempDir(), StatusInterval: time.Second},
		Log: log,
	}
	if err := job.Check(); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	if err := job.Run(ctx, &celery.Message{Task: Task, Args: []any{float64(f.run.ID)}}); err != nil {
		t.Fatal(err)
	}
	t.Logf("drafted, voiced, joined and kept in %v", time.Since(started).Round(time.Millisecond))

	if f.status(t) != statusReady {
		t.Fatalf("the podcast is %q", f.status(t))
	}
	for _, chat := range stub.Chats() {
		if chat.Model != "account-model" {
			t.Fatalf("drafted by %q, not the account's model", chat.Model)
		}
	}
	if len(stub.Chats()) != 3 {
		t.Fatalf("%d drafting calls, want an outline and two beats", len(stub.Chats()))
	}
	if len(stub.Speeches()) != 4 {
		t.Fatalf("%d lines voiced, want each of 4 once", len(stub.Speeches()))
	}

	var (
		key, mime, transcript string
		size, seconds         int64
	)
	if err := f.pool.QueryRow(ctx, `
		SELECT fl.storage_key, fl.mime_type, fl.size_bytes, p.duration_seconds, p.podcast_transcript::text
		FROM podcast_runs p JOIN artifact_files fl ON fl.artifact_id = p.artifact_id
		WHERE p.id = $1`, f.run.ID).Scan(&key, &mime, &size, &seconds, &transcript); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(transcript, "Welcome to the opening beat.") || !strings.Contains(transcript, "the closing.") {
		t.Fatalf("the stored transcript is %s", transcript)
	}
	path := f.files + "/" + key
	audio, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if mime != "audio/mpeg" || int64(len(audio)) != size || !audiofile.IsMP3(audio) {
		t.Fatalf("the kept file is %s, %d bytes (row says %d), MP3=%v", mime, len(audio), size, audiofile.IsMP3(audio))
	}
	length, err := tools.Duration(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// Four lines of 0.6s, each padded a little by its MP3 frames.
	if length < 2.3 || length > 2.8 || seconds < 2 || seconds > 3 {
		t.Fatalf("the episode plays for %.2fs and the row says %ds; want about 2.4s", length, seconds)
	}
	if len(indexer.documents) != 1 {
		t.Fatalf("the transcript was queued for indexing %d times", len(indexer.documents))
	}
}
