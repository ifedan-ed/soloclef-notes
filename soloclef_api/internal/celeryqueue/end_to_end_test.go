package celeryqueue

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// This is the test that says a Python Celery worker runs what this tier
// enqueued: the envelope comparison says the bytes are right, and only a
// worker consuming one says the message is.
//
// It needs a throwaway Redis — it flushes the database it is given — and a
// container with this app's Celery installed, as two URLs because the two
// processes reach that Redis by different names. None is defaulted, so this
// never touches the live broker by accident.
const (
	EnvCeleryContainer = "SOLOCLEF_API_TEST_CELERY_CONTAINER"
	// EnvBrokerFromCelery is the broker as the container sees it;
	// EnvBrokerFromHere is the same server as this test sees it.
	EnvBrokerFromCelery = "SOLOCLEF_API_TEST_BROKER_FROM_CELERY"
	EnvBrokerFromHere   = "SOLOCLEF_API_TEST_BROKER"
)

// The queue and the task are this test's own. A live queue name here would put
// a probe task in front of the workers that serve the product.
const (
	probeQueue = "probe.api.e2e"
	probeTask  = "probe.api.e2e.task"
	probeKey   = "probe.api.e2e.ran"
)

// liveQueues are the three this test must never find in the Redis it flushes
// (soloclef_worker/AGENTS.md).
var liveQueues = []string{"surfsense", "surfsense.connectors", "surfsense.heavy"}

// runProbeWorker is a Celery worker of the smallest kind that is still a real
// one: this app's own Celery and kombu, consuming the queue by name, running
// the body, and recording what reached it — including the queue wait measured
// from this tier's own enqueue stamp, which is the only evidence that the two
// processes read the same clock.
//
// The task stops the worker when it is done, so the script ends on its own
// rather than being killed and leaving a message half-consumed.
const runProbeWorker = `
import json, os, time
from celery import Celery
from celery.exceptions import WorkerShutdown
from redis import Redis

BROKER = os.environ["BROKER_URL"]
app = Celery("probe", broker=BROKER)
assert app.conf.broker_url == BROKER, (
    "broker_url resolved to %r, not the test broker" % (app.conf.broker_url,))
app.conf.update(
    task_serializer="json", accept_content=["json"], result_serializer="json",
    timezone="UTC", enable_utc=True, task_default_queue="probe.api.e2e",
    worker_prefetch_multiplier=1, task_acks_late=True,
)

@app.task(name="probe.api.e2e.task", bind=True)
def probe(self, *args, **kwargs):
    headers = getattr(self.request, "headers", None) or {}
    stamp = headers.get("surfsense.enqueued_at_ns")
    Redis.from_url(BROKER).set("probe.api.e2e.ran", json.dumps({
        "args": list(args),
        "kwargs": kwargs,
        "task": self.request.task,
        "queue": (self.request.delivery_info or {}).get("routing_key"),
        "eta": self.request.eta,
        "retries": self.request.retries,
        "root_id": self.request.root_id,
        "id": self.request.id,
        "queue_wait_ms": None if stamp is None
            else (time.monotonic_ns() - int(stamp)) / 1e6,
    }))
    raise WorkerShutdown()

app.worker_main([
    "worker", "-Q", "probe.api.e2e", "-c", "1", "-P", "solo",
    "--without-gossip", "--without-mingle", "--without-heartbeat", "-l", "ERROR",
])
`

func testBroker(t *testing.T) (valkey.Client, string, string) {
	t.Helper()
	container := os.Getenv(EnvCeleryContainer)
	fromCelery := os.Getenv(EnvBrokerFromCelery)
	fromHere := os.Getenv(EnvBrokerFromHere)
	if container == "" || fromCelery == "" || fromHere == "" {
		t.Skipf("set %s, %s and %s to run the end-to-end test",
			EnvCeleryContainer, EnvBrokerFromCelery, EnvBrokerFromHere)
	}

	option, err := valkey.ParseURL(fromHere)
	if err != nil {
		t.Fatalf("%s: %v", EnvBrokerFromHere, err)
	}
	option.DisableCache = true
	client, err := valkey.NewClient(option)
	if err != nil {
		t.Fatalf("the test broker is unreachable: %v", err)
	}
	t.Cleanup(client.Close)

	// This test flushes, so it refuses to run against anything that looks like
	// the live broker.
	for _, queue := range liveQueues {
		exists, err := client.Do(context.Background(),
			client.B().Exists().Key(queue).Build()).AsInt64()
		if err != nil {
			t.Fatalf("looking for live queues: %v", err)
		}
		if exists != 0 {
			t.Fatalf("%s names a Redis holding the live queue %q", EnvBrokerFromHere, queue)
		}
	}
	flush(t, client)
	t.Cleanup(func() { flush(t, client) })
	return client, container, fromCelery
}

func flush(t *testing.T, client valkey.Client) {
	t.Helper()
	if err := client.Do(context.Background(), client.B().Flushdb().Build()).Error(); err != nil {
		t.Fatalf("flushing the test broker: %v", err)
	}
}

func keysIn(t *testing.T, client valkey.Client) []string {
	t.Helper()
	keys, err := client.Do(context.Background(),
		client.B().Keys().Pattern("*").Build()).AsStrSlice()
	if err != nil {
		t.Fatalf("reading the test broker's keys: %v", err)
	}
	slices.Sort(keys)
	return keys
}

type probeRun struct {
	Args        []any          `json:"args"`
	Kwargs      map[string]any `json:"kwargs"`
	Task        string         `json:"task"`
	Queue       string         `json:"queue"`
	ETA         *string        `json:"eta"`
	Retries     int            `json:"retries"`
	RootID      string         `json:"root_id"`
	ID          string         `json:"id"`
	QueueWaitMS *float64       `json:"queue_wait_ms"`
}

// A Python Celery worker consumes what this tier published, runs it, and gets
// the arguments it was sent — on a Redis where nothing has ever declared
// anything, which is the other half of what this test proves.
func TestAPythonCeleryWorkerRunsWhatThisTierEnqueued(t *testing.T) {
	client, container, brokerFromCelery := testBroker(t)

	if keys := keysIn(t, client); len(keys) != 0 {
		t.Fatalf("the test broker is not empty: %v", keys)
	}

	queue, err := New(os.Getenv(EnvBrokerFromHere), probeQueue)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer queue.Close()

	published, err := queue.Enqueue(context.Background(), produce.Message{
		Task:   probeTask,
		Args:   []any{7, "café — a document", []any{11, 12}},
		Kwargs: map[string]any{"folder_subtree_ids": []any{6, 7}},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Nothing but the queue's own list: no exchange, no _kombu.binding.*, and
	// no declaration of any kind was made or needed to put a message where a
	// Celery worker finds it.
	afterPublish := keysIn(t, client)
	t.Logf("after this tier published: %v", afterPublish)
	if !slices.Equal(afterPublish, []string{probeQueue}) {
		t.Errorf("publishing wrote %v; the queue's list alone is what kombu's own producer leaves behind", afterPublish)
	}

	ran := runWorker(t, container, brokerFromCelery, client)

	if ran.Task != probeTask || ran.ID != published {
		t.Errorf("the worker ran %q/%q, this tier published %q/%q",
			ran.Task, ran.ID, probeTask, published)
	}
	if ran.Queue != probeQueue {
		t.Errorf("the worker saw the queue as %q", ran.Queue)
	}
	if ran.RootID != published {
		t.Errorf("root_id = %q, want the task's own id", ran.RootID)
	}
	if ran.Retries != 0 || ran.ETA != nil {
		t.Errorf("retries %d, eta %v", ran.Retries, ran.ETA)
	}
	// The arguments are what Python's own json decoded them to, text and
	// nesting included: an escape written wrong would arrive wrong here.
	wantArgs := `[7,"café — a document",[11,12]]`
	if got := encode(t, ran.Args); got != wantArgs {
		t.Errorf("args = %s, want %s", got, wantArgs)
	}
	if got := encode(t, ran.Kwargs); got != `{"folder_subtree_ids":[6,7]}` {
		t.Errorf("kwargs = %s", got)
	}
	// The stamp is read by a different process on a different clock reading of
	// the same CLOCK_MONOTONIC, so a wait that is negative or implausible means
	// the two tiers are not measuring the same thing.
	if ran.QueueWaitMS == nil {
		t.Fatal("the worker found no enqueue stamp on the message")
	}
	if *ran.QueueWaitMS <= 0 || *ran.QueueWaitMS > 300000 {
		t.Errorf("the worker measured a queue wait of %.1fms from this tier's stamp", *ran.QueueWaitMS)
	}

	// And the binding table appears only now: it is written by the consumer
	// that declared the queue, which is what makes a producer that declares
	// nothing correct (custom/docs/go-api-tier.md).
	afterConsuming := keysIn(t, client)
	t.Logf("after a Celery worker consumed: %v", afterConsuming)
	bindings := 0
	for _, key := range afterConsuming {
		if strings.HasPrefix(key, "_kombu.binding.") {
			bindings++
		}
	}
	if bindings == 0 {
		t.Errorf("no binding was declared by the worker either: %v", afterConsuming)
	}
}

func runWorker(t *testing.T, container, brokerFromCelery string, client valkey.Client) probeRun {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 4*time.Minute)
	defer stop()

	cmd := exec.CommandContext(ctx, "docker", "exec", "-i",
		"-e", "BROKER_URL="+brokerFromCelery,
		// Celery resolves CELERY_BROKER_URL ahead of its constructor argument,
		// and in the backend container that names the live broker
		// (soloclef_worker/AGENTS.md).
		"-e", "CELERY_BROKER_URL="+brokerFromCelery,
		"-e", "CELERY_RESULT_BACKEND="+brokerFromCelery,
		container, "python", "-")
	cmd.Stdin = strings.NewReader(runProbeWorker)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the probe worker failed: %v\n%s", err, out)
	}

	raw, err := client.Do(context.Background(),
		client.B().Get().Key(probeKey).Build()).ToString()
	if err != nil {
		t.Fatalf("the worker recorded nothing: %v\n%s", err, out)
	}
	var ran probeRun
	if err := json.Unmarshal([]byte(raw), &ran); err != nil {
		t.Fatalf("what the worker recorded is not JSON: %v\n%s", err, raw)
	}
	return ran
}

func encode(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(raw)
}
