package executor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/results"
	"github.com/redis/go-redis/v9"
)

// The whole tier, once, for real: Celery's own client publishes, this worker
// consumes, a real Python task body runs in a process, and Python's
// AsyncResult reads the outcome back. Everything between is the code under
// test; nothing here stands in for anything.
//
// It needs a throwaway Redis and a container running this checkout's backend.
// The queue is its own, and the test refuses a Redis holding a live queue.

const e2eQueue = "probe.exec"

// e2eTask is a registered task body that is cheap and changes nothing:
// reindex_knowledge_store returns 0 at once when KNOWLEDGE_STORE_ENABLED is
// off, without a database connection or a network call, so what it proves is
// the path rather than the work.
const e2eTask = "reindex_knowledge_store"

// publisher is Celery's own client, configured as app/celery_app.py configures
// it, including this app's enqueue stamp. Celery resolves broker_url from
// CELERY_BROKER_URL ahead of its constructor argument, so the URL is asserted
// before anything is published: in a backend container that variable names the
// live broker unless it has been overridden.
const publisher = `
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
    task_routes={
        "reindex_knowledge_store": {"queue": QUEUE},
        "probe.exec.not_a_task": {"queue": QUEUE},
    },
)
print("ran", app.send_task("reindex_knowledge_store", kwargs={"workspace_id": -1}).id)
print("missing", app.send_task("probe.exec.not_a_task").id)
`

// reader is how every client in this app reads a task's outcome. If this does
// not say SUCCESS, the row this worker wrote is not the row Celery writes.
const reader = `
import os, sys
from celery import Celery

app = Celery("soloclef", broker=os.environ["BROKER_URL"], backend=os.environ["BROKER_URL"])
assert app.conf.result_backend == os.environ["BROKER_URL"], (
    "result_backend resolved to %r" % (app.conf.result_backend,))
app.conf.update(result_serializer="json", accept_content=["json"], result_extended=True)

result = app.AsyncResult(sys.argv[1])
print("state", result.state)
print("result", repr(result.result))
print("name", result.name)
print("queue", result.queue)
print("worker", result.worker)
print("exception", type(result.result).__name__)
print("traceback", repr(result.traceback)[:200])
`

func TestARealTaskRunsFromCeleryToAsyncResult(t *testing.T) {
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
	// What the worker will run in its own container is `python -m
	// app.tasks.run`; from outside one, the same command through docker exec.
	cfg.Command = []string{"docker", "exec", "-i", container, "python", "-m", "app.tasks.run"}
	cfg.Worker = "go-worker-e2e"
	cfg.SoftTimeLimit = 0
	cfg.HardTimeLimit = 5 * time.Minute
	cfg.KillGrace = 5 * time.Second
	runner, err := New(cfg, backend)
	if err != nil {
		t.Fatalf("New: %v", err)
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

	published := runInContainer(t, container, brokerFromPython, publisher)
	ran := idFrom(t, published, "ran")
	missing := idFrom(t, published, "missing")

	// A task body here is a Python process importing this app, which takes
	// half a minute before the body itself starts.
	awaitTerminalRow(t, rdb, ran, 4*time.Minute)
	awaitTerminalRow(t, rdb, missing, 4*time.Minute)

	success := runInContainer(t, container, brokerFromPython, reader, ran)
	for _, want := range []string{"state SUCCESS", "result 0", "name " + e2eTask, "queue " + e2eQueue, "worker " + cfg.Worker} {
		if !strings.Contains(success, want) {
			t.Errorf("AsyncResult for the task that ran does not say %q:\n%s", want, success)
		}
	}

	// A task nothing registered is a failed job, not a lost one: the row says
	// so, and says it as the exception Celery would have raised.
	failure := runInContainer(t, container, brokerFromPython, reader, missing)
	// The row carries the exception's own module, so Python rebuilds the real
	// class rather than a stand-in named after it.
	for _, want := range []string{"state FAILURE", "exception NotRegistered"} {
		if !strings.Contains(failure, want) {
			t.Errorf("AsyncResult for the unregistered task does not say %q:\n%s", want, failure)
		}
	}
}

// awaitTerminalRow waits for the row a client would poll to stop being STARTED.
func awaitTerminalRow(t *testing.T, rdb *redis.Client, taskID string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		raw, err := rdb.Get(context.Background(), celery.ResultKey(taskID)).Result()
		if err == nil && !strings.Contains(raw, `"status": "STARTED"`) && !strings.Contains(raw, `"status":"STARTED"`) {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("no terminal row for %s after %v", taskID, within)
}

func runInContainer(t *testing.T, container, brokerURL, script string, args ...string) string {
	t.Helper()
	// CELERY_BROKER_URL and CELERY_RESULT_BACKEND are overridden because
	// Celery reads them in preference to anything its constructor is given.
	argv := []string{
		"exec", "-i",
		"-e", "BROKER_URL=" + brokerURL,
		"-e", "CELERY_BROKER_URL=" + brokerURL,
		"-e", "CELERY_RESULT_BACKEND=" + brokerURL,
		container, "python", "-",
	}
	cmd := exec.Command("docker", append(argv, args...)...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python in %s failed: %v\n%s", container, err, out)
	}
	return string(out)
}

func idFrom(t *testing.T, output, label string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == label {
			return fields[1]
		}
	}
	t.Fatalf("the publisher printed no %s id:\n%s", label, output)
	return ""
}
