package executor

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/results"
)

// The warm pool against the real thing: this app's own import graph, this
// app's own task body, and the saving measured rather than claimed.
//
// Both tests need a container running *this checkout's* backend, with
// app/tasks/run.py and app/tasks/serve.py mounted (soloclef_worker/AGENTS.md).

// warmCommand is what the deployed worker runs: the warm server, which takes
// its jobs on stdin and names no task of its own. Through docker exec here,
// which proves the path but is not a deployment — a signal to the exec client
// never reaches the process group inside the container.
func warmCommand(container string) []string {
	return []string{"docker", "exec", "-i", container, "python", "-m", "app.tasks.serve"}
}

func oneShotCommand(container string) []string {
	return []string{"docker", "exec", "-i", container, "python", "-m", "app.tasks.run"}
}

// The measurement that decided phase 4: how long a job waits before its body
// starts, in a process of its own against a warm one. Phase 3 measured 27-33s
// of imports per job; this runs the same body both ways on this host and says
// what the pool saves.
func TestTheWarmStartSavingAgainstAProcessPerJob(t *testing.T) {
	container := os.Getenv(EnvPythonContainer)
	if container == "" {
		t.Skipf("set %s to a container running this checkout's backend", EnvPythonContainer)
	}

	runs := func(command []string, poolSize, jobs int) []time.Duration {
		cfg := DefaultConfig()
		cfg.Command = command
		cfg.Worker = "go-worker-measure"
		cfg.SoftTimeLimit = 0
		cfg.HardTimeLimit = 5 * time.Minute
		cfg.PoolSize = poolSize
		cfg.ChildReadyTimeout = 3 * time.Minute
		executor, rows := testExecutor(t, cfg)
		defer executor.Close()

		if poolSize > 0 {
			warmed := time.Now()
			if err := executor.Warm(context.Background()); err != nil {
				t.Fatalf("Warm: %v", err)
			}
			t.Logf("the warm process imported this app in %v", time.Since(warmed).Round(time.Millisecond))
		}

		var took []time.Duration
		for job := range jobs {
			msg := freshMessage(t, "measured-"+string(rune('a'+job)))
			msg.Task = e2eTask
			msg.Args = nil
			msg.Kwargs = map[string]any{"workspace_id": -1}
			started := time.Now()
			if err := executor.Run(context.Background(), msg); err != nil {
				t.Fatalf("job %d: %v", job, err)
			}
			took = append(took, time.Since(started))
		}
		if row := rows.terminal(t); row.State != celery.StateSuccess {
			t.Fatalf("the last row is %s, so the body did not run: %#v", row.State, row.Value)
		}
		return took
	}

	perJob := runs(oneShotCommand(container), 0, 2)
	warm := runs(warmCommand(container), 1, 3)

	t.Logf("a process per job: %v", perJob)
	t.Logf("a warm process:    %v", warm)

	slowest := warm[0]
	for _, took := range warm[1:] {
		if took > slowest {
			slowest = took
		}
	}
	fastestPerJob := perJob[0]
	for _, took := range perJob[1:] {
		if took < fastestPerJob {
			fastestPerJob = took
		}
	}
	t.Logf("the saving is %v a job, from %v to at most %v",
		(fastestPerJob - slowest).Round(time.Millisecond), fastestPerJob.Round(time.Millisecond),
		slowest.Round(time.Millisecond))
	if slowest > fastestPerJob/4 {
		t.Errorf("a warm job took %v against %v in a process of its own; the pool is not saving the imports",
			slowest, fastestPerJob)
	}
}

// The whole tier on the warm shape: Celery's own client publishes three jobs,
// this worker consumes them, one warm process runs all three bodies, and
// Python's AsyncResult reads every row back.
func TestWarmRealTasksRunFromCeleryToAsyncResult(t *testing.T) {
	container := os.Getenv(EnvPythonContainer)
	brokerFromPython := os.Getenv(EnvTestRedisFromPython)
	if container == "" || brokerFromPython == "" {
		t.Skipf("set %s and %s to run the end-to-end test", EnvPythonContainer, EnvTestRedisFromPython)
	}
	rdb := testRedis(t)

	backend, err := results.NewBackend(rdb, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Command = warmCommand(container)
	cfg.Worker = "go-worker-warm-e2e"
	cfg.SoftTimeLimit = 0
	cfg.HardTimeLimit = 5 * time.Minute
	cfg.KillGrace = 5 * time.Second
	cfg.PoolSize = 1
	cfg.ChildReadyTimeout = 3 * time.Minute
	runner, err := New(cfg, backend)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(runner.Close)
	if err := runner.Warm(context.Background()); err != nil {
		t.Fatalf("Warm: %v", err)
	}

	brokerCfg := broker.DefaultConfig()
	brokerCfg.Queues = []string{e2eQueue}
	brokerCfg.QueueConcurrency = map[string]int{e2eQueue: 1}
	brokerCfg.Name = cfg.Worker
	brokerCfg.ShutdownGrace = 2 * time.Second
	consumer, err := broker.NewConsumer(rdb, brokerCfg, runner)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	consuming := make(chan error, 1)
	go func() { consuming <- consumer.Run(ctx) }()
	t.Cleanup(func() {
		stop()
		<-consuming
	})

	// The pool is warm, so three jobs cost three bodies rather than three
	// import graphs: a minute is generous for the first and absurd for the
	// 81s of imports a process per job would have paid.
	started := time.Now()
	published := runInContainer(t, container, brokerFromPython, warmPublisher)
	ids := []string{idFrom(t, published, "first"), idFrom(t, published, "second"), idFrom(t, published, "third")}
	for _, id := range ids {
		awaitTerminalRow(t, rdb, id, time.Minute)
	}
	t.Logf("three real bodies in a warm process took %v", time.Since(started).Round(time.Millisecond))

	for _, id := range ids {
		read := runInContainer(t, container, brokerFromPython, reader, id)
		for _, want := range []string{"state SUCCESS", "result 0", "name " + e2eTask, "worker " + cfg.Worker} {
			if !strings.Contains(read, want) {
				t.Errorf("AsyncResult for %s does not say %q:\n%s", id, want, read)
			}
		}
	}
}

// warmPublisher is Celery's own client, as app/celery_app.py configures it,
// publishing three jobs for one warm process to run.
const warmPublisher = `
import os, time
from celery import Celery
from celery.signals import before_task_publish

@before_task_publish.connect
def _stamp_enqueue_time(headers=None, **_kwargs):
    if headers is not None:
        headers["surfsense.enqueued_at_ns"] = str(time.monotonic_ns())

QUEUE = "probe.exec"
app = Celery("soloclef", broker=os.environ["BROKER_URL"], backend=os.environ["BROKER_URL"])
assert app.conf.broker_url == os.environ["BROKER_URL"], (
    "broker_url resolved to %r, not the test broker" % (app.conf.broker_url,))
app.conf.update(
    task_serializer="json", accept_content=["json"], result_serializer="json",
    task_default_queue=QUEUE, task_default_exchange=QUEUE,
    task_default_routing_key=QUEUE, task_acks_late=True,
    task_reject_on_worker_lost=True, task_track_started=True,
    result_extended=True, result_expires=600,
    task_routes={"reindex_knowledge_store": {"queue": QUEUE}},
)
for label in ("first", "second", "third"):
    print(label, app.send_task("reindex_knowledge_store", kwargs={"workspace_id": -1}).id)
`
