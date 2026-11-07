package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/celery"
)

// The soft limit as Celery delivers it: the body is signalled, raises
// SoftTimeLimitExceeded, and its own except block reports the failure.
func TestTheSoftLimitAsksTheBodyToStop(t *testing.T) {
	command := fakeChild(t, `
trap 'echo "{\"status\": \"FAILURE\", \"exc_type\": \"SoftTimeLimitExceeded\", \"exc_module\": \"celery.exceptions\", \"exc_message\": [\"SoftTimeLimitExceeded(0.2,)\"]}"; exit 1' USR1
sleep 30 &
wait
`)
	cfg := testConfig(t, command)
	cfg.SoftTimeLimit = 200 * time.Millisecond
	cfg.HardTimeLimit = 20 * time.Second
	executor, rows := testExecutor(t, cfg)

	if err := executor.Run(context.Background(), probeMessage(t)); err == nil {
		t.Fatal("a task stopped by its soft limit has failed")
	}
	row := rows.terminal(t)
	if row.State != celery.StateFailure {
		t.Fatalf("state = %s", row.State)
	}
	if got := errorField(t, row, "exc_type"); got != excSoftTimeLimit {
		t.Errorf("exc_type = %q, want %q", got, excSoftTimeLimit)
	}
}

// A body that is signalled and dies without reporting still gets the row the
// soft limit earns it, rather than one about a lost worker.
func TestASoftLimitDecidesTheRowWhenTheBodyGoesQuiet(t *testing.T) {
	command := fakeChild(t, `
trap 'exit 7' USR1
sleep 30 &
wait
`)
	cfg := testConfig(t, command)
	cfg.SoftTimeLimit = 200 * time.Millisecond
	cfg.HardTimeLimit = 20 * time.Second
	executor, rows := testExecutor(t, cfg)

	if err := executor.Run(context.Background(), probeMessage(t)); err == nil {
		t.Fatal("the task did not finish its work")
	}
	if got := errorField(t, rows.terminal(t), "exc_type"); got != excSoftTimeLimit {
		t.Errorf("exc_type = %q, want %q", got, excSoftTimeLimit)
	}
}

// Finishing in the last moment before a deadline is finishing: the body's own
// report wins over the limit that was already going off.
func TestATaskThatFinishesJustInTimeSucceeds(t *testing.T) {
	command := fakeChild(t, `
trap '' USR1
sleep 0.4
echo '{"status": "SUCCESS", "result": "just in time"}'
`)
	cfg := testConfig(t, command)
	cfg.SoftTimeLimit = 100 * time.Millisecond
	cfg.HardTimeLimit = 10 * time.Second
	executor, rows := testExecutor(t, cfg)

	if err := executor.Run(context.Background(), probeMessage(t)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	row := rows.terminal(t)
	if row.State != celery.StateSuccess || row.Value != "just in time" {
		t.Errorf("row = %s / %#v, want the body's own success", row.State, row.Value)
	}
}

// The hard limit on a body that will not go: SIGTERM is ignored, SIGKILL is
// not, and the row names the limit that ended it.
func TestTheHardLimitKillsABodyThatIgnoresSigterm(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	command := fakeChild(t, `
trap '' TERM
echo $$ > `+pidFile+`
sleep 60 &
wait
`)
	cfg := testConfig(t, command)
	cfg.HardTimeLimit = 400 * time.Millisecond
	cfg.KillGrace = 200 * time.Millisecond
	executor, rows := testExecutor(t, cfg)

	if err := executor.Run(context.Background(), probeMessage(t)); err == nil {
		t.Fatal("a task killed at its hard limit has not succeeded")
	}
	if got := errorField(t, rows.terminal(t), "exc_type"); got != excHardTimeLimit {
		t.Errorf("exc_type = %q, want %q", got, excHardTimeLimit)
	}
	waitGone(t, pidFrom(t, pidFile), 5*time.Second)
}

// The reason the group is signalled rather than the process: a Python body
// spawns ffmpeg, and killing only the body would leave the encoder running.
func TestKillingATaskKillsWhatItStarted(t *testing.T) {
	dir := t.TempDir()
	grandchildPID := filepath.Join(dir, "grandchild")
	command := fakeChild(t, `
trap '' TERM
sleep 120 &
echo $! > `+grandchildPID+`
sleep 120 &
wait
`)
	cfg := testConfig(t, command)
	cfg.HardTimeLimit = 400 * time.Millisecond
	cfg.KillGrace = 200 * time.Millisecond
	executor, _ := testExecutor(t, cfg)

	pid := make(chan int, 1)
	go func() { pid <- pidFrom(t, grandchildPID) }()

	if err := executor.Run(context.Background(), probeMessage(t)); err == nil {
		t.Fatal("the task should have been killed at its hard limit")
	}
	waitGone(t, <-pid, 5*time.Second)
}

// Shutdown: the worker's context is cancelled, the group is asked to stop, and
// the client gets a terminal row because the delivery will be acknowledged.
func TestShutdownStopsTheTaskAndStillWritesARow(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	command := fakeChild(t, `
echo $$ > `+pidFile+`
sleep 120 &
wait
`)
	executor, rows := testExecutor(t, testConfig(t, command))

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- executor.Run(ctx, probeMessage(t)) }()
	pid := pidFrom(t, pidFile)
	stop()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a task cut off by a shutdown has not succeeded")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after the worker was asked to stop")
	}
	if got := errorField(t, rows.terminal(t), "exc_type"); got != excTerminated {
		t.Errorf("exc_type = %q, want %q", got, excTerminated)
	}
	waitGone(t, pid, 5*time.Second)
}

// The producer's own timelimit header wins, and the process is told the same
// numbers the worker is enforcing.
func TestTheMessagesOwnLimitsWinAndTheProcessIsToldThem(t *testing.T) {
	captured := filepath.Join(t.TempDir(), "stdin.json")
	command := fakeChild(t, `
cat > `+captured+`
echo '{"status": "SUCCESS", "result": null}'
`)
	cfg := testConfig(t, command)
	cfg.SoftTimeLimit = time.Hour
	cfg.HardTimeLimit = 2 * time.Hour
	executor, _ := testExecutor(t, cfg)

	msg := probeMessage(t)
	msg.SoftTimeLimit = 90 * time.Second
	msg.HardTimeLimit = 120 * time.Second
	if err := executor.Run(context.Background(), msg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	raw, err := os.ReadFile(captured)
	if err != nil {
		t.Fatalf("reading the call: %v", err)
	}
	var call Call
	if err := json.Unmarshal(raw, &call); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if call.SoftTimeLimit != 90 || call.HardTimeLimit != 120 {
		t.Errorf("limits = %v / %v, want the message's own", call.SoftTimeLimit, call.HardTimeLimit)
	}
}

// A soft limit at or past the hard one could never fire first; pretending
// otherwise would hide which limit ended a job.
func TestASoftLimitPastTheHardOneIsDropped(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SoftTimeLimit = time.Hour
	cfg.HardTimeLimit = 2 * time.Hour

	msg := &celery.Message{SoftTimeLimit: 3 * time.Hour}
	if lim := cfg.limitsFor(msg); lim.soft != 0 || lim.hard != 2*time.Hour {
		t.Errorf("limits = %v / %v", lim.soft, lim.hard)
	}
}
