package videojob

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/audiofile"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/llm"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

// The variables that point the end-to-end test at a real OpenSandbox server
// and the sandbox image, the names the studio's own real-server tests use.
// The server must be a throwaway: the test creates and removes sandboxes.
const (
	realSandboxURL   = "SOLOCLEF_TEST_SANDBOX_URL"
	realSandboxKey   = "SOLOCLEF_TEST_SANDBOX_KEY"
	realSandboxImage = "SOLOCLEF_TEST_SANDBOX_IMAGE"
)

// stubProxy answers the two things the job asks the proxy: the author's
// draft (a streamed chat completion) and each scene's voice (a real MP3 of
// a few seconds, from ffmpeg). Any other chat completion is counted: the
// layout of the scenes is checked in the sandbox, and nothing should ask a
// model to look at them.
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
			http.Error(w, "no model looks at the stills", http.StatusBadRequest)
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

// TestEndToEnd makes a two-scene video in a real sandbox from the real
// image, each way it can be assembled, through the real claim, the real
// rows and a real file store, with the proxy stubbed. It logs how long each
// stage took.
func TestEndToEnd(t *testing.T) {
	address := os.Getenv(realSandboxURL)
	if address == "" {
		t.Skipf("%s is not set: this test needs a throwaway opensandbox/server and the soloclef-sandbox-local image", realSandboxURL)
	}
	pool := testdatabase.Open(t)
	tools, err := audiofile.Find()
	if err != nil {
		t.Skip("no ffmpeg:", err)
	}
	client, err := sandbox.New(sandbox.Settings{
		// A stack of this run's own, so nothing it makes is any deployment's.
		Stack: "test-video-" + strconv.FormatInt(time.Now().UnixNano(), 10), URL: address, APIKey: os.Getenv(realSandboxKey), Image: os.Getenv(realSandboxImage),
		CPU: "4", Memory: "6Gi", IdleTTL: 10 * time.Minute, CommandTimeout: 5 * time.Minute, MaxPerAccount: 1,
	}, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{ModeFrames, ModeStills} {
		t.Run(mode, func(t *testing.T) {
			owner := testdatabase.Account(t, pool)
			job, _ := queuedVideo(t, pool, owner)
			proxy := startStubProxy(t, draftReply(
				"The heart has four chambers that work as two pumps.",
				"Four valves keep the blood moving one way."))
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
				Rows: Store{Pool: pool}, Sandboxes: SandboxClient{Client: client}, Models: writers,
				Voicer: voicer, Audio: tools, Files: files, Indexer: &fakeIndexer{},
				Config: Config{Enabled: true, Mode: mode, RenderTimeout: 20 * time.Minute, RenderConcurrency: 4,
					FramesPerSegment: 1800, FrameTimeout: 30 * time.Second, MaxRenders: 1,
					Voice: "marin", VoiceID: "openai:marin",
					Sandbox: sandbox.Settings{CommandTimeout: 5 * time.Minute}},
				Timing: Timing{CallTimeout: time.Minute, StatusInterval: 2 * time.Second, KeepAlive: time.Minute,
					Claim: ClaimRules{StaleAfter: 10 * time.Minute, MaxTakeovers: 2}, RepairCycles: 2, NarrationConcurrency: 4},
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
				WHERE a.id = $1`, *artifact).Scan(&key, &seconds); err != nil {
				t.Fatal(err)
			}
			stored, err := files.Open(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			mp4, _ := io.ReadAll(stored)
			stored.Close()
			measured, err := tools.Seconds(context.Background(), mp4)
			// Two scenes of 3.2 s of narration: 6.4 s on the frame grid.
			if err != nil || measured < 6.2 || measured > 7 || seconds < 6.2 || seconds > 7 {
				t.Errorf("the stored video plays %.2fs (%v); its metadata says %.1fs", measured, err, seconds)
			}
			if proxy.drafted != 1 || proxy.spoken != 2 || proxy.looked != 0 {
				t.Errorf("drafted %d, spoke %d, asked a model to look %d times", proxy.drafted, proxy.spoken, proxy.looked)
			}
			// The attempt's sandbox is gone with it.
			if boxes, err := client.ForJob(context.Background(), owner, job.sandboxName()); err == nil {
				_ = client.TerminateJob(context.Background(), job.sandboxName())
				_ = boxes
			}
		})
	}
}
