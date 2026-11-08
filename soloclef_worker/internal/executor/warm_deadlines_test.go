package executor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
)

// The limits have to bite on a warm process exactly as they do on a process
// started for one job, and a process they stopped must never run another body.

// The hard limit on a body that will not go. SIGTERM is ignored, SIGKILL is
// not, the row names the limit, and the next job gets a process of its own.
func TestTheHardLimitKillsAWarmProcessThatIgnoresSigterm(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pids")
	command := fakeWarmChild(t, "trap '' TERM", `
echo $$ >> `+pidFile+`
if [ "$id" = stuck ]; then sleep 60 & wait; fi
succeed
`)
	cfg := testPoolConfig(t, command)
	cfg.HardTimeLimit = 500 * time.Millisecond
	cfg.KillGrace = 200 * time.Millisecond
	executor, rows := testExecutor(t, cfg)
	t.Cleanup(executor.Close)

	if err := executor.Run(context.Background(), freshMessage(t, "stuck")); err == nil {
		t.Fatal("a task killed at its hard limit has not succeeded")
	}
	if got := errorField(t, rows.terminal(t), "exc_type"); got != excHardTimeLimit {
		t.Errorf("exc_type = %q, want %q", got, excHardTimeLimit)
	}
	killed := pidsFrom(t, pidFile)[0]
	waitGone(t, killed, 5*time.Second)

	// The process that was killed is gone, so the next job cannot be given to
	// it: a stopped process is retired, never reused.
	if err := executor.Run(context.Background(), freshMessage(t, "after")); err != nil {
		t.Fatalf("the job after a killed one: %v", err)
	}
	if pids := pidsFrom(t, pidFile); len(pids) != 2 || pids[0] == pids[1] {
		t.Errorf("pids = %v, want the next job in a process of its own", pids)
	}
}

// The reason the group is signalled rather than the process, and the reason a
// warm process is no safer: a body spawns ffmpeg, and killing only the body
// would leave the encoder running with nothing watching it.
//
// Mutation-tested: making askGroupToStop/killGroup signal the process instead
// of its group (kill(pid) rather than kill(-pid) in signals_unix.go) leaves
// the grandchild running and fails this test.
func TestKillingAWarmJobKillsWhatTheBodyStarted(t *testing.T) {
	dir := t.TempDir()
	grandchildPID := filepath.Join(dir, "grandchild")
	command := fakeWarmChild(t, "trap '' TERM", `
sleep 120 &
echo $! > `+grandchildPID+`
sleep 120 & wait
`)
	cfg := testPoolConfig(t, command)
	cfg.HardTimeLimit = 500 * time.Millisecond
	cfg.KillGrace = 200 * time.Millisecond
	executor, _ := testExecutor(t, cfg)
	t.Cleanup(executor.Close)

	pid := make(chan int, 1)
	go func() { pid <- pidFrom(t, grandchildPID) }()

	if err := executor.Run(context.Background(), freshMessage(t, "renders")); err == nil {
		t.Fatal("the task should have been killed at its hard limit")
	}
	waitGone(t, <-pid, 5*time.Second)
}

// The soft limit as Celery delivers it: SIGUSR1 to the process, the body
// raises, and its own except block reports the failure. The process survives
// it, as a prefork child does, and runs the next job.
func TestTheSoftLimitAsksAWarmBodyToStopWithoutEndingTheProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	command := fakeWarmChild(t, "", `
echo $$ >> `+pidFile+`
if [ "$ran" = 1 ]; then
  trap 'report "{\"status\":\"FAILURE\",\"exc_type\":\"SoftTimeLimitExceeded\",\"exc_module\":\"celery.exceptions\",\"exc_message\":[\"SoftTimeLimitExceeded(0.3,)\"]}"; trap - USR1; continue' USR1
  sleep 30 & wait
fi
succeed
`)
	cfg := testPoolConfig(t, command)
	cfg.SoftTimeLimit = 300 * time.Millisecond
	cfg.HardTimeLimit = 20 * time.Second
	executor, rows := testExecutor(t, cfg)
	t.Cleanup(executor.Close)

	if err := executor.Run(context.Background(), freshMessage(t, "slow")); err == nil {
		t.Fatal("a task stopped by its soft limit has failed")
	}
	if got := errorField(t, rows.terminal(t), "exc_type"); got != excSoftTimeLimit {
		t.Errorf("exc_type = %q, want %q", got, excSoftTimeLimit)
	}

	if err := executor.Run(context.Background(), freshMessage(t, "next")); err != nil {
		t.Fatalf("the job after a soft limit: %v", err)
	}
	if pids := pidsFrom(t, pidFile); len(pids) != 2 || pids[0] != pids[1] {
		t.Errorf("pids = %v, want the same process: a soft limit is the body's to handle", pids)
	}
}

// A process that dies holding a job is what an OOM kill looks like. Celery
// calls it WorkerLostError and so must this, because nothing of the body's own
// ran: whatever it was holding is still held.
func TestAWarmProcessThatDiesMidJobIsALostWorker(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	command := fakeWarmChild(t, "", `
echo $$ >> `+pidFile+`
if [ "$id" = doomed ]; then
  echo 'about to be killed' >&2
  kill -9 $$
  sleep 5
fi
succeed
`)
	executor, rows := testExecutor(t, testPoolConfig(t, command))
	t.Cleanup(executor.Close)

	if err := executor.Run(context.Background(), freshMessage(t, "doomed")); err == nil {
		t.Fatal("a job whose process was killed is not a success")
	}
	row := rows.terminal(t)
	if row.State != celery.StateFailure {
		t.Fatalf("state = %s", row.State)
	}
	if got := errorField(t, row, "exc_type"); got != excWorkerLost {
		t.Errorf("exc_type = %q, want %q", got, excWorkerLost)
	}
	message, _ := row.Value.(map[string]any)["exc_message"].([]any)
	if len(message) == 0 || !strings.Contains(message[0].(string), "signal 9") {
		t.Errorf("the row should name the signal, got %#v", message)
	}
	if !strings.Contains(row.Traceback, "about to be killed") {
		t.Errorf("the row should carry what the process last logged, got %q", row.Traceback)
	}

	// And the pool replaced it, so the next job runs.
	if err := executor.Run(context.Background(), freshMessage(t, "after")); err != nil {
		t.Fatalf("the job after a lost process: %v", err)
	}
	if pids := pidsFrom(t, pidFile); len(pids) != 2 || pids[0] == pids[1] {
		t.Errorf("pids = %v, want a replacement process", pids)
	}
}

// Shutdown: the worker's context is cancelled, the job's process group is
// stopped, and the client still gets a terminal row because the delivery will
// be acknowledged.
func TestShutdownStopsAWarmJobAndStillWritesARow(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	command := fakeWarmChild(t, "", `
echo $$ >> `+pidFile+`
sleep 120 & wait
`)
	executor, rows := testExecutor(t, testPoolConfig(t, command))
	t.Cleanup(executor.Close)

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- executor.Run(ctx, freshMessage(t, "cut-off")) }()
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

// A process that takes a job and says nothing is a process that must not be
// given another: the job ends at its hard limit, and the process ends with it.
func TestAWedgedProcessIsNotGivenAnotherJob(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	command := fakeWarmChild(t, "", `
echo $$ >> `+pidFile+`
sleep 120 & wait
`)
	cfg := testPoolConfig(t, command)
	cfg.HardTimeLimit = 400 * time.Millisecond
	cfg.KillGrace = 100 * time.Millisecond
	executor, _ := testExecutor(t, cfg)
	t.Cleanup(executor.Close)

	for _, id := range []string{"wedged-1", "wedged-2"} {
		if err := executor.Run(context.Background(), freshMessage(t, id)); err == nil {
			t.Fatalf("%s: a job its process never reported on is not a success", id)
		}
	}
	if pids := pidsFrom(t, pidFile); len(pids) != 2 || pids[0] == pids[1] {
		t.Errorf("pids = %v, want each job in a process of its own", pids)
	}
}
