package executor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

// The point of the pool: the imports are paid once, not once per job. Three
// jobs, one process, and every one of them a success row.
func TestAWarmProcessRunsJobAfterJob(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	command := fakeWarmChild(t, "", `
echo $$ >> `+pidFile+`
succeed
`)
	executor, rows := testExecutor(t, testPoolConfig(t, command))
	t.Cleanup(executor.Close)

	for _, id := range []string{"job-1", "job-2", "job-3"} {
		if err := executor.Run(context.Background(), freshMessage(t, id)); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}

	if pids := pidsFrom(t, pidFile); len(pids) != 3 || distinct(pids) != 1 {
		t.Errorf("pids = %v, want three jobs in one process", pids)
	}
	if rows.terminal(t).Value != "ok" {
		t.Errorf("the last row is %#v, want the body's own value", rows.terminal(t).Value)
	}
}

// What the pool is for, measured: only the first job waits for the process to
// start, and the import cost is never paid again.
func TestOnlyTheFirstJobWaitsForTheImports(t *testing.T) {
	const imports = 2 * time.Second
	command := fakeWarmChild(t, "sleep 2", "succeed")
	executor, _ := testExecutor(t, testPoolConfig(t, command))
	t.Cleanup(executor.Close)

	run := func(id string) time.Duration {
		started := time.Now()
		if err := executor.Run(context.Background(), freshMessage(t, id)); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return time.Since(started)
	}

	if first := run("cold"); first < imports {
		t.Fatalf("the first job took %v, so it never waited for the process to start", first)
	}
	if warm := run("warm"); warm > imports/2 {
		t.Errorf("a warm job took %v; it should not pay the %v of imports again", warm, imports)
	}
}

// Celery replaces a prefork child after worker_max_tasks_per_child bodies.
// The same bargain here: a process runs its share and then a fresh one does.
func TestAProcessThatHasRunItsShareOfJobsIsReplaced(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	command := fakeWarmChild(t, "", `
echo $$ >> `+pidFile+`
succeed
`)
	cfg := testPoolConfig(t, command)
	cfg.MaxJobsPerChild = 2
	executor, _ := testExecutor(t, cfg)
	t.Cleanup(executor.Close)

	for _, id := range []string{"job-1", "job-2", "job-3", "job-4"} {
		if err := executor.Run(context.Background(), freshMessage(t, id)); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}

	pids := pidsFrom(t, pidFile)
	if len(pids) != 4 || distinct(pids) != 2 {
		t.Errorf("pids = %v, want two processes running two jobs each", pids)
	}
	if pids[0] != pids[1] || pids[2] != pids[3] || pids[1] == pids[2] {
		t.Errorf("pids = %v, want the replacement to arrive after the second job", pids)
	}
}

// A body that leaks is the case worker_max_tasks_per_child cannot catch early
// enough: this process really does grow, job after job, and the ceiling is
// what retires it before the host feels it.
func TestAProcessThatGrowsPastTheCeilingIsReplaced(t *testing.T) {
	pids := func(ceiling uint64) []int {
		pidFile := filepath.Join(t.TempDir(), "pids")
		command := fakeWarmChild(t, `hoard=$(head -c 2000000 /dev/zero | tr '\0' 'x')`, `
hoard="$hoard$hoard"
echo $$ >> `+pidFile+`
succeed
`)
		cfg := testPoolConfig(t, command)
		cfg.MaxJobsPerChild = 0
		cfg.MaxChildRSS = ceiling
		executor, _ := testExecutor(t, cfg)
		t.Cleanup(executor.Close)

		for job := range 6 {
			id := fmt.Sprintf("grow-%d", job)
			if err := executor.Run(context.Background(), freshMessage(t, id)); err != nil {
				t.Fatalf("%s: %v", id, err)
			}
		}
		return pidsFrom(t, pidFile)
	}

	// The same workload twice: with a ceiling the growing process is replaced,
	// and without one nothing retires it, so the ceiling is what did it.
	if grew := pids(24 << 20); distinct(grew) < 2 {
		t.Errorf("pids = %v, want a process that outgrew the ceiling replaced", grew)
	}
	if unbounded := pids(0); distinct(unbounded) != 1 {
		t.Errorf("pids = %v, want no replacement with no ceiling", unbounded)
	}
}

// A process out of step with its work cannot be trusted with the next job:
// the report it would write would be attributed to the wrong task.
func TestAProcessThatReportsAJobNobodyIsWaitingForIsReplaced(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	command := fakeWarmChild(t, "", `
echo $$ >> `+pidFile+`
succeed
if [ "$ran" = 1 ]; then echo '{"id":"a-job-from-the-past","report":{"status":"SUCCESS","result":"stale"}}'; fi
`)
	executor, _ := testExecutor(t, testPoolConfig(t, command))
	t.Cleanup(executor.Close)

	for _, id := range []string{"job-1", "job-2"} {
		if err := executor.Run(context.Background(), freshMessage(t, id)); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if pids := pidsFrom(t, pidFile); distinct(pids) != 2 {
		t.Errorf("pids = %v, want the process that reported a stale job replaced", pids)
	}
}

// Two jobs at once need two processes: the pool is what bounds how many warm
// interpreters exist, and the broker's own limits are what bound the work.
func TestConcurrentJobsGetAProcessEach(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	command := fakeWarmChild(t, "", `
echo $$ >> `+pidFile+`
sleep 0.5 & wait
succeed
`)
	cfg := testPoolConfig(t, command)
	cfg.PoolSize = 2
	executor, _ := testExecutor(t, cfg)
	t.Cleanup(executor.Close)

	var running sync.WaitGroup
	for _, id := range []string{"both-1", "both-2"} {
		running.Add(1)
		go func() {
			defer running.Done()
			if err := executor.Run(context.Background(), freshMessage(t, id)); err != nil {
				t.Errorf("%s: %v", id, err)
			}
		}()
	}
	running.Wait()

	if pids := pidsFrom(t, pidFile); distinct(pids) != 2 {
		t.Errorf("pids = %v, want a process each", pids)
	}
}

// A warm process outlives its jobs, so something has to end it: a worker that
// stopped consuming must not leave a Python interpreter behind.
func TestClosingTheExecutorEndsTheWarmProcesses(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	executor, _ := testExecutor(t, testPoolConfig(t, fakeWarmChild(t, "", `
echo $$ >> `+pidFile+`
succeed
`)))
	if err := executor.Run(context.Background(), freshMessage(t, "job-1")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	pid := pidsFrom(t, pidFile)[0]

	executor.Close()
	waitGone(t, pid, 5*time.Second)
}

// The imports are paid at startup when they are asked for, so the first job of
// the day does not pay them.
func TestWarmingReadiesTheProcessesBeforeTheFirstJob(t *testing.T) {
	executor, _ := testExecutor(t, testPoolConfig(t, fakeWarmChild(t, "sleep 1", "succeed")))
	t.Cleanup(executor.Close)

	if err := executor.Warm(context.Background()); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	started := time.Now()
	if err := executor.Run(context.Background(), freshMessage(t, "job-1")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if took := time.Since(started); took > 500*time.Millisecond {
		t.Errorf("the first job took %v, so the pool was not warm", took)
	}
}

// A command that is not the warm server never says it is ready. Nothing ran,
// so the message is still work.
func TestAProcessThatNeverSaysItIsReadyHandsTheMessageBack(t *testing.T) {
	cfg := testPoolConfig(t, fakeChild(t, `sleep 30 & wait`))
	cfg.ChildReadyTimeout = 300 * time.Millisecond
	executor, rows := testExecutor(t, cfg)
	t.Cleanup(executor.Close)

	err := executor.Run(context.Background(), freshMessage(t, "job-1"))
	if !errors.Is(err, broker.ErrRequeue) {
		t.Fatalf("err = %v, want the message handed back", err)
	}
	if len(rows.all()) != 0 {
		t.Errorf("rows = %#v, want none: nothing ran", rows.all())
	}
}

// A job waiting for a process must not wait on one that is never coming. When
// a retired process cannot be replaced — the Python side is broken, the host
// is out of memory — the job that was queued behind it has to be free to try
// for itself and then hand its message back, rather than holding a claim until
// the worker shuts down.
func TestAJobWaitingForAProcessIsNotStuckWhenNoneCanBeStarted(t *testing.T) {
	// The first process works; every one after it starts and says nothing,
	// which is how a broken entrypoint looks from here.
	started := filepath.Join(t.TempDir(), "started-once")
	command := fakeChild(t, `
if [ -f `+started+` ]; then sleep 30 & wait; fi
: > `+started+`
echo '{"ready":true,"pid":'$$'}'
while IFS= read -r line; do
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  sleep 0.5 & wait
  echo '{"id":"'"$id"'","report":{"status":"SUCCESS","result":"ok"}}'
done
`)
	cfg := testPoolConfig(t, command)
	cfg.MaxJobsPerChild = 1
	cfg.ChildReadyTimeout = 500 * time.Millisecond
	cfg.StartFailureDelay = 10 * time.Millisecond
	executor, _ := testExecutor(t, cfg)
	t.Cleanup(executor.Close)

	first := make(chan error, 1)
	go func() { first <- executor.Run(context.Background(), freshMessage(t, "first")) }()

	// Long enough that the second job finds the only process busy and waits
	// for one, and far longer than it should need once it stops waiting.
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	time.Sleep(100 * time.Millisecond)
	waited := time.Now()
	err := executor.Run(ctx, freshMessage(t, "second"))
	took := time.Since(waited)

	if !errors.Is(err, broker.ErrRequeue) {
		t.Fatalf("err = %v, want the message handed back", err)
	}
	if took > 5*time.Second {
		t.Errorf("the waiting job took %v to give up, so it was waiting on a process nothing was going to hand back", took)
	}
	if err := <-first; err != nil {
		t.Errorf("the first job: %v", err)
	}
}
