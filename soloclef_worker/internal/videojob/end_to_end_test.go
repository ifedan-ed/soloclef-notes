package videojob

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/audiofile"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/llm"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

// The variables that point the end-to-end test at a real resvg, and at a
// directory to keep the video it makes in, to be looked at.
const (
	realResvg = "SOLOCLEF_TEST_RESVG"
	keepVideo = "SOLOCLEF_TEST_KEEP_VIDEO"
)

// stubProxy answers the two things the job asks the proxy: the author's
// deck (a streamed chat completion) and each passage's voice (a real MP3 of
// a few seconds, from ffmpeg). Any other chat completion is counted:
// nothing should ask a model to look at a video.
type stubProxy struct {
	*httptest.Server
	mu      sync.Mutex
	draft   string
	tone    []byte
	looked  int
	spoken  int
	drafted int
}

func startStubProxy(t *testing.T, draft string) *stubProxy {
	t.Helper()
	tone := filepath.Join(t.TempDir(), "tone.mp3")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=330:duration=3.2", "-ac", "1", "-ar", "24000", "-b:a", "64k", tone).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	mp3, _ := os.ReadFile(tone)
	s := &stubProxy{draft: draft, tone: mp3}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/audio/speech", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		s.spoken++
		s.mu.Unlock()
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(s.tone)
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &request)
		if !request.Stream {
			s.mu.Lock()
			s.looked++
			s.mu.Unlock()
			http.Error(w, "no model looks at a video", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.drafted++
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": s.draft}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// TestEndToEnd makes a three-slide video with the real resvg and ffmpeg,
// through the real claim, the real rows and a real file store, with the
// proxy stubbed (a fixed deck, a tone for every voice). It logs how long
// each stage took, and keeps the video in SOLOCLEF_TEST_KEEP_VIDEO.
func TestEndToEnd(t *testing.T) {
	binary := os.Getenv(realResvg)
	if binary == "" {
		t.Skipf("%s is not set: this test draws real frames", realResvg)
	}
	pool := testdatabase.Open(t)
	tools, err := audiofile.Find()
	if err != nil {
		t.Skip("no ffmpeg:", err)
	}
	raster, err := deckrender.NewRasteriser(binary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner := testdatabase.Account(t, pool)
	job, _ := queuedVideo(t, pool, owner)
	deck := map[string]any{
		"title": "The heart", "theme": "night",
		"slides": []any{
			map[string]any{"kind": "title", "title": "How the heart pumps", "subtitle": "Four chambers, four valves, one direction",
				"narration": []string{"This is how the heart pumps.", "Four chambers and four valves keep blood moving one way."}},
			map[string]any{"kind": "diagram", "style": "flow", "title": "One beat, step by step", "steps": []any{
				map[string]any{"label": "Atria fill", "text": "Blood returns from the body and lungs"},
				map[string]any{"label": "Atria squeeze", "text": "Blood is pushed into the ventricles"},
				map[string]any{"label": "Ventricles squeeze", "text": "Blood leaves for the lungs and the body"}},
				"narration": []string{"One beat happens in three steps.", "First the atria fill.", "Then they squeeze.", "Then the ventricles push blood out."}},
			map[string]any{"kind": "stats", "title": "A lifetime of beats", "stats": []any{
				map[string]any{"value": "72", "label": "beats a minute at rest"}, map[string]any{"value": "5 L", "label": "of blood pumped every minute"}},
				"narration": []string{"The numbers are large.", "About seventy-two beats a minute.", "Five litres every minute."}},
		},
	}
	draft, _ := json.Marshal(deck)
	proxy := startStubProxy(t, string(draft))
	files, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	service := speech.Service{Roster: speech.OpenAI, Model: "gpt-4o-mini-tts"}
	voicer := speech.Client{BaseURL: proxy.URL + "/v1", Key: "k", Service: service, HTTP: http.DefaultClient, Audio: tools,
		AttemptTimeout: time.Minute, MaxAttempts: 2, Backoff: time.Second, Log: log}
	writers := llm.Writers{Choices: llm.Choices{Pool: pool, Default: "chat"},
		Proxy: llm.Proxy{BaseURL: proxy.URL + "/v1", Key: "k", HTTP: http.DefaultClient, CallTimeout: time.Minute,
			Retry: model.RetryPolicy{Attempts: 1, Backoff: time.Second, MaxBackoff: time.Second}}}
	var stages []Stage
	video := &Job{
		Rows: Store{Pool: pool}, Raster: raster, Models: writers,
		Voicer: voicer, Audio: tools, Files: files, Indexer: &fakeIndexer{}, Notifier: &recordingNotifier{},
		Config: Config{Enabled: true, Resvg: binary, FrameWorkers: 4, RenderTimeout: 10 * time.Minute, MaxRenders: 1,
			Voice: "marin", VoiceID: "openai:marin"},
		Timing: Timing{CallTimeout: time.Minute, StatusInterval: 2 * time.Second, KeepAlive: time.Minute,
			Claim: ClaimRules{StaleAfter: 10 * time.Minute, MaxTakeovers: 2}, NarrationConcurrency: 4},
		Log:      log,
		Measured: func(_ int64, measured []Stage) { stages = measured },
	}
	if err := video.Check(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = video.Run(context.Background(), &celery.Message{Task: Task, ID: job.TaskID, Kwargs: map[string]any{"job_id": float64(job.ID)}})
	total := time.Since(started)
	var internal string
	_ = pool.QueryRow(context.Background(), `SELECT COALESCE(internal_error, '') FROM deliverable_jobs WHERE id = $1`, job.ID).Scan(&internal)
	if err != nil {
		t.Fatalf("%v\n%s", err, internal)
	}
	status, _, _, artifact := row(t, pool, job.ID)
	if status != "ready" || artifact == nil {
		t.Fatalf("row %s, artifact %v: %s", status, artifact, internal)
	}
	for _, stage := range stages {
		t.Logf("%-12s %s", stage.Name, stage.Took.Round(time.Millisecond))
	}
	t.Logf("%-12s %s", "total", total.Round(time.Millisecond))

	var key string
	var seconds float64
	if err := pool.QueryRow(context.Background(), `
		SELECT f.storage_key, (a.metadata ->> 'duration_seconds')::float8 FROM artifact_files f JOIN artifacts a ON a.id = f.artifact_id
		WHERE a.id = $1 AND f.role = 'primary'`, *artifact).Scan(&key, &seconds); err != nil {
		t.Fatal(err)
	}
	stored, err := files.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	mp4, _ := io.ReadAll(stored)
	stored.Close()
	measured, err := tools.Seconds(context.Background(), mp4)
	if err != nil || measured < 9 || math.Abs(measured-seconds) > 0.2 {
		t.Errorf("the stored video plays %.2fs (%v); its metadata says %.1fs", measured, err, seconds)
	}
	// One speech call per passage: two, four and three.
	if proxy.drafted != 1 || proxy.spoken != 9 || proxy.looked != 0 {
		t.Errorf("drafted %d, spoke %d, asked a model to look %d times", proxy.drafted, proxy.spoken, proxy.looked)
	}
	if dir := os.Getenv(keepVideo); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "end-to-end.mp4"), mp4, 0o644)
	}
}
