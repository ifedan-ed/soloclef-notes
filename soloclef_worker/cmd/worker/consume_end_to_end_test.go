package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/automationrun"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/drivesync"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/proxystub"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testredis"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/videojob"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/workerconfig"
)

type lpush struct{ rdb *redis.Client }

func (l lpush) LPush(ctx context.Context, key, value string) error {
	return l.rdb.LPush(ctx, key, value).Err()
}

// The worker as it is wired: messages published the way the API tier
// publishes them, taken off two queues by the real consumer, dispatched by
// the job table this command builds, and run to the rows the apps read.
func TestTheWorkerRunsWhatTheAPIPublishes(t *testing.T) {
	// A Redis database of this package's own, emptied (internal/testredis).
	rdb := testredis.Open(t, testredis.WorkerCommand)
	pool := testdatabase.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	owner := testdatabase.Account(t, pool)
	var podcastID, notebookID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO podcast_runs (user_id, title, status, source_content, spec)
		VALUES ($1::uuid, 'Asthma', 'drafting', 'Asthma is a chronic airway disease.', $2::jsonb) RETURNING id`, owner,
		`{"language":"en","style":"conversational","speakers":[{"slot":0,"name":"Ada","role":"host","voice_id":"openai:marin"},`+
			`{"slot":1,"name":"Ben","role":"guest","voice_id":"openai:cedar"}],"duration":{"min_seconds":20,"max_seconds":30}}`).Scan(&podcastID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO notebooks (user_id, title, title_source) VALUES ($1::uuid, 'Untitled notebook', 'auto') RETURNING id`, owner).Scan(&notebookID); err != nil {
		t.Fatal(err)
	}

	stub := proxystub.Start()
	defer stub.Close()
	stub.Chat = func(request proxystub.ChatRequest) (string, int) {
		switch system := request.System(); {
		case strings.Contains(system, "Name a research notebook"):
			return `{"title": "asthma in adults", "icon": "🩺"}`, http.StatusOK
		case strings.Contains(system, "showrunner"):
			return `{"segments":[{"title":"All of it","talking_points":["what asthma is"],"target_words":60}]}`, http.StatusOK
		default:
			return `{"turns":[{"speaker":0,"text":"What is asthma?"},{"speaker":1,"text":"A chronic airway disease."}]}`, http.StatusOK
		}
	}
	stub.Speech = func(proxystub.SpeechRequest) ([]byte, int) { return proxystub.Tone(0.5), http.StatusOK }

	keyFile := t.TempDir() + "/key"
	_ = os.WriteFile(keyFile, []byte("k"), 0o600)
	service, _ := speech.ReadService("openai/gpt-4o-mini-tts")
	files, err := filestore.OpenStores(filestore.Settings{Backend: filestore.LocalBackend, LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	settings := workerconfig.Config{
		ProxyURL: stub.BaseURL(), ProxyKey: "k", ChatModel: "chat", QuickModel: "quick", CallTimeout: 10 * time.Second,
		Speech: service, SpeechAttemptTimeout: 10 * time.Second, SpeechMaxAttempts: 2, SpeechConcurrency: 4,
		PodcastWorkDirectory: t.TempDir(), QdrantURL: "http://127.0.0.1:1", QdrantAlias: "chunks", QdrantTimeout: time.Second,
		DefaultQueue: "probe", SecretKey: "fake-deployment-secret",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The three feature switches have no default and the job table reads
	// them from the environment itself, so the test says them as a
	// deployment must. All off: this test is about the podcast and the
	// notebook's name, and each switched-on feature needs a service of its
	// own that is tested where it lives.
	for _, feature := range []string{videojob.Enabled, automationrun.Enabled, drivesync.Enabled} {
		t.Setenv(feature, "false")
	}
	runner, err := table(settings, pool, rdb, files, log)
	if err != nil {
		t.Fatal(err)
	}
	routes := routing.Table{Default: "probe"}
	cfg := broker.DefaultConfig()
	cfg.Name = "probe-worker"
	cfg.Queues = routes.Queues()
	cfg.QueueConcurrency = map[string]int{"probe": 2, "probe.heavy": 2}
	consumer, err := broker.NewConsumer(rdb, cfg, runner)
	if err != nil {
		t.Fatal(err)
	}

	producer := produce.Producer{Broker: lpush{rdb}, Routes: routes, Origin: produce.Origin(), ReplyTo: "probe"}
	published := time.Now()
	// The podcast routes' and the chat's own messages (internal/podcasts,
	// internal/chat/mentions.go).
	if _, err := producer.Send(ctx, produce.Delay("podcast.draft_transcript", podcastID)); err != nil {
		t.Fatal(err)
	}
	if _, err := producer.Send(ctx, produce.Message{Task: "notebook_autoname", Args: []any{notebookID, "question", "What triggers asthma attacks in adults?"}}); err != nil {
		t.Fatal(err)
	}

	running, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- consumer.Run(running) }()
	defer func() {
		stop()
		<-done
	}()

	var status, title string
	for {
		_ = pool.QueryRow(ctx, `SELECT status::text FROM podcast_runs WHERE id = $1`, podcastID).Scan(&status)
		_ = pool.QueryRow(ctx, `SELECT title FROM notebooks WHERE id = $1`, notebookID).Scan(&title)
		if status == "ready" && title != "Untitled notebook" {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("the podcast is %q and the notebook %q after a minute", status, title)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("both jobs done %v after they were published", time.Since(published).Round(time.Millisecond))
	if title != "Asthma in Adults" {
		t.Fatalf("the notebook is named %q", title)
	}
	// The episode's transcript was handed to the indexer, on its own queue.
	if n, _ := rdb.LLen(ctx, "soloclef.index").Result(); n != 1 {
		t.Fatalf("%d index_document messages for the indexer, want 1", n)
	}
}
