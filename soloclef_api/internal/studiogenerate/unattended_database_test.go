package studiogenerate

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange/livechangetest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
)

// boaster plays a specialist that loads a report's instructions (which
// needs a sandbox) and then, whatever it was told, claims a save: the
// answer that Studio job 33 showed its owner on 14 Nov 2026.
type boaster struct{}

const boast = "Saved a 15-slide presenter deck titled Tides. Nothing was stored, as it happens."

func (boaster) Generate(_ context.Context, request model.Request, _ func(model.Chunk)) (model.Response, error) {
	for _, message := range request.Messages {
		if message.Role == model.RoleTool {
			return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: boast}, FinishReason: "stop"}, nil
		}
	}
	return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
		{ID: "c1", Name: deliverables.InstructionsTool, Args: `{"artifact_type":"pdf"}`},
	}}, FinishReason: "tool_calls"}, nil
}

type boasterModels struct{}

func (boasterModels) For(context.Context, string) (model.Model, string, error) {
	return boaster{}, "test-model", nil
}

// fullAccount is a sandbox service whose account stays full: a bench asked
// with a wait reports the wait, holds until the test lets it go, and is
// refused as busy. Releases are recorded.
type fullAccount struct {
	mu       sync.Mutex
	waited   []time.Duration
	released []int64
	waiting  chan struct{}
	letGo    chan struct{}
}

func (f *fullAccount) Check() error { return nil }
func (f *fullAccount) Bench(ctx context.Context, owner deliverables.Owner, wait deliverables.SandboxWait) (deliverables.Bench, error) {
	f.mu.Lock()
	f.waited = append(f.waited, wait.For)
	f.mu.Unlock()
	if wait.Waiting != nil {
		wait.Waiting(ctx, true)
	}
	close(f.waiting)
	<-f.letGo
	if wait.Waiting != nil {
		wait.Waiting(context.WithoutCancel(ctx), false)
	}
	sentence, _ := deliverables.FailureSentence(deliverables.FailureSandboxBusy)
	return nil, &deliverables.WorkshopRefusal{Reason: sentence, Code: deliverables.FailureSandboxBusy}
}
func (f *fullAccount) Release(_ context.Context, owner deliverables.Owner) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, owner.ThreadID)
	return nil
}

// A run whose account has no sandbox free waits, and its row says so while
// it does; when the wait runs out it fails with the server's code and
// sentence, never the model's claim of a save, and its sandbox's place is
// given back.
func TestARunWaitsForASandboxAndFailsInTheServersWords(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title) VALUES (10, '`+alice+`', 'Tides');
INSERT INTO new_chat_threads (id, user_id, title, notebook_id, source) VALUES (70, '`+alice+`', 'Report', 10, 'artifact_api');
INSERT INTO deliverable_jobs (id, user_id, kind, title, thread_id, tool_call_id, status, request) VALUES
 (33, '`+alice+`', 'artifact', 'Report: Tides', 70, 'job-33', 'queued',
  '{"version":2,"output":"report","format":"pdf","task":"Kind of artifact: report."}');`)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	local, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	account := &fullAccount{waiting: make(chan struct{}), letGo: make(chan struct{})}
	specialist := deliverables.Specialist{
		Store: checkpoint.NewMemory(), Limits: deliverables.Limits{MaxSteps: 8, Timeout: time.Minute},
		Workshop: account, Verified: deliverables.NewVerifications(), Podcasts: noPodcasts{},
		Saver: deliverables.Saver{Rows: deliverables.Store{Pool: pool}, Files: local, Logger: quiet,
			NewID: func() string { return "00000000000000000000000000000abc" },
			Index: func(context.Context, int64, string) error { return nil }},
	}
	lifetime, stop := context.WithCancel(context.Background())
	changes := &livechangetest.Recorder{}
	notifier := &notify.Notifier{Database: pool, Publisher: &recordedPushes{}, Changes: changes, Log: quiet}
	runner, err := NewRunner(RunnerParts{Pool: pool, Models: boasterModels{}, Specialist: specialist, Notifier: notifier, Changes: changes,
		Concurrent: 2, Accepting: lifetime, Lifetime: lifetime, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stop(); runner.Wait() })
	h := &harness{t: t, pool: pool}

	// The job is queued in its row; the runner claims it as it starts.
	runner.Run()
	<-account.waiting
	h.until("the row says it waits", "deliverable_jobs WHERE id = 33 AND status = 'running' AND phase = 'waiting_for_sandbox'")
	// Waiting holds no database connection: the pool has none in use.
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Errorf("%d connections held while the run waits", acquired)
	}
	close(account.letGo)

	h.until("the run fails as busy", "deliverable_jobs WHERE id = 33 AND status = 'failed' AND failure_code = 'sandbox_busy'")
	if h.count("deliverable_jobs WHERE id = 33 AND (internal_error LIKE '%Saved%' OR phase <> 'failed')") != 0 {
		t.Error("the model's claim reached the row, or the phase was not closed")
	}
	busy, _ := deliverables.FailureSentence(deliverables.FailureSandboxBusy)
	h.until("the owner is told the server's sentence", `notifications WHERE user_id = '`+alice+`' AND type = 'studio_output'
		AND message LIKE '%`+strings.ReplaceAll(busy, "'", "''")+`' AND metadata ->> 'error_message' = '`+strings.ReplaceAll(busy, "'", "''")+`'`)
	if h.count("notifications WHERE message LIKE '%Saved a 15-slide%'") != 0 {
		t.Error("the model's claim reached the notification")
	}
	// The run has ended and given its sandbox back once the runner is idle.
	// (Wait is for after the stop: it also waits for the claiming loop.)
	<-runner.Idle()
	account.mu.Lock()
	defer account.mu.Unlock()
	if len(account.waited) != 1 || account.waited[0] != deliverables.UnattendedSandboxWait {
		t.Errorf("the bench was asked to wait %v, want %s once", account.waited, deliverables.UnattendedSandboxWait)
	}
	if len(account.released) != 1 || account.released[0] != 70 {
		t.Errorf("released %v, want the run's thread 70", account.released)
	}
}

// The phase is written only over a running job: a job being stopped keeps
// `cancelling`'s own phase.
func TestThePhaseIsWrittenOnlyOverARunningJob(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	testpostgres.Exec(t, pool, `
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, phase) VALUES
 (1, '`+alice+`', 'artifact', 'Running', 'a', 'running', 'generating'),
 (2, '`+alice+`', 'artifact', 'Stopping', 'b', 'cancelling', 'generating'),
 (3, '`+alice+`', 'video', 'A video', 'c', 'running', 'rendering');`)
	waiting := phaseWaitingForSandbox
	for _, id := range []int64{1, 2, 3} {
		if err := New(pool).SetRunningPhase(context.Background(), SetRunningPhaseParams{Phase: &waiting, Now: time.Now(), ID: id, UserID: alice, Attempt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := New(pool).SetRunningPhase(context.Background(), SetRunningPhaseParams{Phase: &waiting, Now: time.Now(), ID: 1, UserID: bob, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, pool: pool}
	if h.count("deliverable_jobs WHERE phase = 'waiting_for_sandbox'") != 1 || h.count("deliverable_jobs WHERE id = 1 AND phase = 'waiting_for_sandbox'") != 1 {
		t.Error("only the running artifact job of its owner should take the phase")
	}
}
