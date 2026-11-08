package executor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

// A failure the body raised must read back as that exception, not as a worker
// error: the type, the module and the traceback are what a reader has to work
// with.
func TestAFailedBodyKeepsItsOwnException(t *testing.T) {
	command := fakeChild(t, `
echo 'traceback (most recent call last)' >&2
echo '{"status": "FAILURE", "exc_type": "KnowledgeStoreLockError", "exc_module": "app.knowledge_store.locks", "exc_message": ["store is locked"], "traceback": "Traceback: ..."}'
exit 1
`)
	executor, rows := testExecutor(t, testConfig(t, command))

	err := executor.Run(context.Background(), probeMessage(t))
	if err == nil {
		t.Fatal("a failed task must come back as an error")
	}

	row := rows.terminal(t)
	if row.State != celery.StateFailure {
		t.Fatalf("state = %s", row.State)
	}
	if got := errorField(t, row, "exc_type"); got != "KnowledgeStoreLockError" {
		t.Errorf("exc_type = %q", got)
	}
	if got := errorField(t, row, "exc_module"); got != "app.knowledge_store.locks" {
		t.Errorf("exc_module = %q", got)
	}
	if row.Traceback != "Traceback: ..." {
		t.Errorf("traceback = %q", row.Traceback)
	}
}

// A retry is the body publishing its own next attempt, so this delivery is
// finished and the row says RETRY, as Celery's does.
func TestARetryingBodyGetsARetryRow(t *testing.T) {
	command := fakeChild(t, `
echo '{"status": "RETRY", "exc_type": "KnowledgeStoreLockError", "exc_module": "app.knowledge_store.locks", "exc_message": ["locked"]}'
exit 1
`)
	executor, rows := testExecutor(t, testConfig(t, command))

	err := executor.Run(context.Background(), probeMessage(t))
	if err == nil {
		t.Fatal("a retry is not a success")
	}
	if errors.Is(err, broker.ErrRequeue) {
		t.Fatal("the body already published its retry; the broker must not put this message back as well")
	}
	if row := rows.terminal(t); row.State != celery.StateRetry {
		t.Errorf("state = %s, want RETRY", row.State)
	}
}

// A process that writes something unreadable has run the body. Saying so, with
// its last log lines, is the only honest row.
func TestAnUnreadableReportIsSaidToBeUnreadable(t *testing.T) {
	command := fakeChild(t, `
echo 'the last thing the task said' >&2
echo '{"status": "SUCC'
`)
	executor, rows := testExecutor(t, testConfig(t, command))

	if err := executor.Run(context.Background(), probeMessage(t)); err == nil {
		t.Fatal("an unreadable report is not a success")
	}
	row := rows.terminal(t)
	if got := errorField(t, row, "exc_type"); got != excUnreadable {
		t.Errorf("exc_type = %q, want %q", got, excUnreadable)
	}
	if !strings.Contains(row.Traceback, "the last thing the task said") {
		t.Errorf("the row should carry what the process last logged, got %q", row.Traceback)
	}
}

func TestAProcessThatSaysNothingAtAllIsNotASuccess(t *testing.T) {
	executor, rows := testExecutor(t, testConfig(t, fakeChild(t, `exit 0`)))

	if err := executor.Run(context.Background(), probeMessage(t)); err == nil {
		t.Fatal("silence is not a result")
	}
	if got := errorField(t, rows.terminal(t), "exc_type"); got != excUnreadable {
		t.Errorf("exc_type = %q, want %q", got, excUnreadable)
	}
}

// What an OOM kill looks like: no report, no exit code, a signal. Celery calls
// it WorkerLostError and so must this, because nothing of the body's own ran.
func TestAProcessKilledOutrightIsReportedAsALostWorker(t *testing.T) {
	command := fakeChild(t, `
echo 'about to be killed' >&2
kill -9 $$
sleep 5
`)
	executor, rows := testExecutor(t, testConfig(t, command))

	if err := executor.Run(context.Background(), probeMessage(t)); err == nil {
		t.Fatal("a killed process is not a success")
	}
	row := rows.terminal(t)
	if got := errorField(t, row, "exc_type"); got != excWorkerLost {
		t.Errorf("exc_type = %q, want %q", got, excWorkerLost)
	}
	message, _ := row.Value.(map[string]any)["exc_message"].([]any)
	if len(message) == 0 || !strings.Contains(message[0].(string), "signal 9") {
		t.Errorf("the row should name the signal, got %#v", message)
	}
}

func TestANonZeroExitWithoutAReportIsALostWorker(t *testing.T) {
	executor, rows := testExecutor(t, testConfig(t, fakeChild(t, `exit 3`)))

	if err := executor.Run(context.Background(), probeMessage(t)); err == nil {
		t.Fatal("exit 3 is not a success")
	}
	row := rows.terminal(t)
	if got := errorField(t, row, "exc_type"); got != excWorkerLost {
		t.Errorf("exc_type = %q, want %q", got, excWorkerLost)
	}
}

// Nothing ran, so nothing failed: the message is still work and goes back.
func TestAProcessThatCannotBeStartedHandsTheMessageBack(t *testing.T) {
	cfg := testConfig(t, []string{filepath.Join(t.TempDir(), "no-such-command")})
	executor, rows := testExecutor(t, cfg)

	err := executor.Run(context.Background(), probeMessage(t))
	if !errors.Is(err, broker.ErrRequeue) {
		t.Fatalf("Run = %v, want a requeue", err)
	}
	if written := rows.all(); len(written) != 0 {
		t.Errorf("rows = %#v, want none: the task never started", written)
	}
}

// Celery stores no row at all for a task declared ignore_result, so a SUCCESS
// row here would be a state Python would never have shown.
func TestATaskWhoseResultIsIgnoredGetsNoRows(t *testing.T) {
	command := fakeChild(t, `echo '{"status": "SUCCESS", "result": null}'`)
	executor, rows := testExecutor(t, testConfig(t, command))
	msg := probeMessage(t)
	msg.IgnoreResult = true

	if err := executor.Run(context.Background(), msg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if written := rows.all(); len(written) != 0 {
		t.Errorf("rows = %#v, want none", written)
	}
}

// A body that never reads its call must not be able to wedge the worker, and a
// call bigger than a pipe's buffer is how that happens.
func TestAProcessThatNeverReadsItsCallStillFinishes(t *testing.T) {
	command := fakeChild(t, `echo '{"status": "SUCCESS", "result": "done"}'`)
	executor, rows := testExecutor(t, testConfig(t, command))
	msg := probeMessage(t)
	msg.Kwargs = map[string]any{"source_content": strings.Repeat("x", 512<<10)}

	done := make(chan error, 1)
	go func() { done <- executor.Run(context.Background(), msg) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the worker is stuck feeding a process that is not listening")
	}
	if row := rows.terminal(t); row.State != celery.StateSuccess {
		t.Errorf("state = %s", row.State)
	}
}
