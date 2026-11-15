package studiogenerate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange/livechangetest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// The variables that point the test below at a real sandbox service
// (soloclef_sandboxd) with the stack's sandbox image, which has LibreOffice.
// It makes and removes sandboxes of its own account; it must not be a
// deployment's service, whose people's sandboxes would count beside them.
const (
	sandboxURLVariable = "SOLOCLEF_API_TEST_STUDIOGENERATE_SANDBOX_URL"
	sandboxKeyVariable = "SOLOCLEF_API_TEST_STUDIOGENERATE_SANDBOX_KEY"
)

// decker plays a specialist that saves a slide deck, whose preview is drawn
// by LibreOffice in the run's sandbox.
type decker struct{ deck json.RawMessage }

func (d decker) Generate(_ context.Context, request model.Request, _ func(model.Chunk)) (model.Response, error) {
	results := 0
	for _, message := range request.Messages {
		if message.Role == model.RoleTool {
			results++
		}
	}
	call := func(name, args string) (model.Response, error) {
		return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: fmt.Sprintf("c%d", results+1), Name: name, Args: args}}}, FinishReason: "tool_calls"}, nil
	}
	switch results {
	case 0:
		return call(deliverables.InstructionsTool, `{"artifact_type":"pptx"}`)
	case 1:
		args, _ := json.Marshal(map[string]any{"format": "pptx", "title": "Photosynthesis", "deck": d.deck})
		return call(deliverables.SaveTool, string(args))
	}
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Made a deck."}, FinishReason: "stop"}, nil
}

type deckerModels struct{ decker }

func (d deckerModels) For(context.Context, string) (model.Model, string, error) {
	return d.decker, "test-model", nil
}

// More decks than the account's limit of sandboxes, started at once, all
// finish: those that find the limit reached wait, show it on their row, and
// get a sandbox when another deck finishes and gives its back. Waiting
// holds no database connection.
func TestMoreDecksThanTheSandboxLimitAllFinish(t *testing.T) {
	address := os.Getenv(sandboxURLVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a sandbox service of its own", sandboxURLVariable)
	}
	const limit, decks = 2, 6
	pool := testpostgres.Open(t, databaseVariable)
	testpostgres.Exec(t, pool, `INSERT INTO notebooks (id, user_id, title) VALUES (10, '`+alice+`', 'Leaves');`)
	for job := 1; job <= decks; job++ {
		testpostgres.Exec(t, pool, fmt.Sprintf(`
INSERT INTO new_chat_threads (id, user_id, title, notebook_id, source) VALUES (%d, '%s', 'Deck', 10, 'artifact_api');
INSERT INTO deliverable_jobs (id, user_id, kind, title, thread_id, tool_call_id, status, request) VALUES (%d, '%s', 'artifact', 'Deck %d', %d, 'job-%d', 'queued',
  '{"version":2,"output":"slides","format":"pptx","task":"Kind of artifact: slides."}');`,
			800+job, alice, job, alice, job, 800+job, job))
	}
	deck, err := os.ReadFile("../../../soloclef_studio/deckrender/testdata/photosynthesis.json")
	if err != nil {
		t.Fatal(err)
	}
	client, err := sandbox.New(sandbox.Settings{
		URL: address, APIKey: os.Getenv(sandboxKeyVariable), CPU: "1", Memory: "2Gi",
		IdleTTL: 5 * time.Minute, CommandTimeout: 3 * time.Minute, MaxPerAccount: limit,
	}, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	logs := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("SOLOCLEF_TEST_VERBOSE") != "" {
		logs = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	local, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	specialist := deliverables.Specialist{
		Store: checkpoint.NewMemory(), Limits: deliverables.Limits{MaxSteps: 8, Timeout: 10 * time.Minute},
		Workshop: deliverables.Sandboxes{Client: client}, Verified: deliverables.NewVerifications(), Podcasts: noPodcasts{},
		Saver: deliverables.Saver{Rows: deliverables.Store{Pool: pool}, Files: local, Logger: logs,
			NewID: func() string { return fmt.Sprintf("%032x", time.Now().UnixNano()) },
			Index: func(context.Context, int64, string) error { return nil }},
	}
	lifetime, stop := context.WithCancel(context.Background())
	changes := &livechangetest.Recorder{}
	notifier := &notify.Notifier{Database: pool, Publisher: &recordedPushes{}, Changes: changes, Log: logs}
	// As many runs at once as there are decks, so the limit, not the
	// runner, is what makes some wait.
	runner, err := NewRunner(RunnerParts{Pool: pool, Models: deckerModels{decker{deck: deck}}, Specialist: specialist,
		Notifier: notifier, Changes: changes, Concurrent: decks, Accepting: lifetime, Lifetime: lifetime, Logger: logs})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stop(); runner.Wait() })

	before := runtime.NumGoroutine()
	started := time.Now()
	// The jobs are queued in their rows; the runner claims them all as it
	// starts, having a place for each.
	runner.Run()
	// Sample while it runs: the most jobs seen waiting at once, and the
	// most database connections in use while any did.
	var mu sync.Mutex
	mostWaiting, connsWhileWaiting, goroutinesWhileWaiting := 0, int32(0), 0
	h := &harness{t: t, pool: pool}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for time.Since(started) < 9*time.Minute {
			var waiting, open int
			if err := pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE phase = 'waiting_for_sandbox'),
				count(*) FILTER (WHERE status IN ('queued', 'running')) FROM deliverable_jobs`).Scan(&waiting, &open); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			if waiting > mostWaiting {
				mostWaiting = waiting
			}
			if waiting > 0 {
				// The sampler's own query has just returned its connection.
				connsWhileWaiting = max(connsWhileWaiting, pool.Stat().AcquiredConns())
				goroutinesWhileWaiting = max(goroutinesWhileWaiting, runtime.NumGoroutine()-before)
			}
			mu.Unlock()
			if open == 0 {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	<-done
	elapsed := time.Since(started)
	if ready := h.count("deliverable_jobs WHERE status = 'ready' AND artifact_id IS NOT NULL"); ready != decks {
		var failures []string
		rows, _ := pool.Query(context.Background(), `SELECT id, status, coalesce(failure_code, ''), coalesce(internal_error, '') FROM deliverable_jobs ORDER BY id`)
		for rows.Next() {
			var id int64
			var status, code, detail string
			_ = rows.Scan(&id, &status, &code, &detail)
			failures = append(failures, fmt.Sprintf("%d %s %s %s", id, status, code, detail))
		}
		rows.Close()
		t.Fatalf("%d of %d decks ready after %s:\n%v", ready, decks, elapsed, failures)
	}
	mu.Lock()
	defer mu.Unlock()
	if mostWaiting == 0 {
		t.Error("no deck ever waited: the test did not reach the limit")
	}
	// Each deck's card heard its claim and its ending, and a deck that
	// waited heard its wait start and end: the open Studio panel follows
	// "waiting for a sandbox" without a reload.
	outputs := 0
	for _, call := range changes.Calls() {
		for _, change := range call.Changes {
			if change.Kind == livechange.Outputs {
				outputs++
			}
		}
	}
	if outputs < 2*decks+2 {
		t.Errorf("%d Studio changes told for %d decks, some of which waited; want at least %d", outputs, decks, 2*decks+2)
	}
	t.Logf("%d decks against a limit of %d: all ready in %s; at most %d waiting at once, %d database connections and %d goroutines over the start held while any waited",
		decks, limit, elapsed.Round(time.Millisecond), mostWaiting, connsWhileWaiting, goroutinesWhileWaiting)
}
