package broker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
)

// These tests are the ones that decide whether phase 4 can be gradual: they
// run real Celery and real kombu against the same throwaway Redis as this
// worker, and check that each can recover a message the other was holding.
//
// They need a container with Celery installed and a URL for the test Redis as
// that container sees it. All three variables are required, and none is
// defaulted, so this never runs against the live broker by accident.
const (
	EnvCeleryContainer  = "SOLOCLEF_WORKER_TEST_CELERY_CONTAINER"
	EnvTestRedisInThere = "SOLOCLEF_WORKER_TEST_REDIS_FROM_CELERY"
)

const interopQueue = "probe.interop"

// celeryProducer is the app's producer, rebuilt: the same Celery version, the
// same serializer settings, and the same before_task_publish stamp as
// app/celery_app.py. Importing the app itself would pull in the live
// configuration, which these tests must not touch.
//
// Celery resolves broker_url from the CELERY_BROKER_URL environment variable
// before anything passed to its constructor, and the container this runs in
// has that set to the live broker. The assertion below is the guard: a
// mis-set environment fails here instead of publishing to the live queues.
const celeryProducer = `
import os, time
from celery import Celery
from celery.signals import before_task_publish

@before_task_publish.connect
def _stamp_enqueue_time(headers=None, **_kwargs):
    if headers is not None:
        headers["surfsense.enqueued_at_ns"] = str(time.monotonic_ns())

QUEUE = "probe.interop"
app = Celery("soloclef", broker=os.environ["BROKER_URL"])
assert app.conf.broker_url == os.environ["BROKER_URL"], (
    "broker_url resolved to %r, not the test broker" % (app.conf.broker_url,))
app.conf.update(
    task_serializer="json", accept_content=["json"], result_serializer="json",
    task_default_queue=QUEUE, task_default_exchange=QUEUE,
    task_default_routing_key=QUEUE, task_acks_late=True,
    task_reject_on_worker_lost=True, worker_prefetch_multiplier=1,
    task_track_started=True,
    task_routes={"probe.interop.task": {"queue": QUEUE}},
)
app.send_task("probe.interop.task", args=[1, "two"], kwargs={"k": True})
print("sent")
`

// kombuRestore is kombu's own stale-claim scan, with the window set to zero so
// every claim counts as stale. This is the code a Python Celery worker runs on
// a timer; pointing it at a claim this worker made is the test.
const kombuRestore = `
import os
from kombu import Connection

QUEUE = "probe.interop"
with Connection(os.environ["BROKER_URL"],
                transport_options={"visibility_timeout": 0}) as conn:
    channel = conn.channel()
    channel.exchange_declare(QUEUE, type="direct")
    channel.queue_declare(QUEUE)
    channel.queue_bind(QUEUE, QUEUE, QUEUE)
    before = channel.client.hlen("unacked")
    channel.qos.restore_visible(num=100, interval=1)
    print("restored", before - channel.client.hlen("unacked"))
`

// kombuTakeAndDie is a Python worker killed mid-job: it takes a message, which
// registers the claim, and then exits without running the interpreter's
// shutdown hooks, so the claim is left exactly as a SIGKILL leaves it.
//
// The process must die without closing the channel: kombu's own
// restore_at_shutdown would otherwise put the message back, which is the
// clean-shutdown path rather than the lost-worker one.
const kombuTakeAndDie = `
import os
from kombu import Connection, Queue

QUEUE = "probe.interop"
conn = Connection(os.environ["BROKER_URL"])
channel = conn.channel()
channel.exchange_declare(QUEUE, type="direct")
queue = Queue(QUEUE, channel=channel)
queue.declare()
message = queue.get(no_ack=False)
print("took", message.properties["delivery_tag"] if message else "nothing", flush=True)
os._exit(0)
`

// kombuBindings reads the binding table restoreKey's assumption rests on: that
// a queue's routing key names the queue it is bound to.
const kombuBindings = `
import os
from kombu import Connection

QUEUE = "probe.interop"
with Connection(os.environ["BROKER_URL"]) as conn:
    channel = conn.channel()
    channel.exchange_declare(QUEUE, type="direct")
    channel.queue_declare(QUEUE)
    channel.queue_bind(QUEUE, QUEUE, QUEUE)
    print("|".join(channel._lookup(QUEUE, QUEUE)))
`

func runPython(t *testing.T, script string) string {
	t.Helper()
	container := os.Getenv(EnvCeleryContainer)
	brokerURL := os.Getenv(EnvTestRedisInThere)
	if container == "" || brokerURL == "" {
		t.Skipf("set %s and %s to run the Celery interop tests", EnvCeleryContainer, EnvTestRedisInThere)
	}
	// CELERY_BROKER_URL and CELERY_RESULT_BACKEND are overridden because
	// Celery reads them in preference to anything its constructor is given,
	// and in this container they name the live broker.
	cmd := exec.Command("docker", "exec", "-i",
		"-e", "BROKER_URL="+brokerURL,
		"-e", "CELERY_BROKER_URL="+brokerURL,
		"-e", "CELERY_RESULT_BACKEND="+brokerURL,
		container, "python", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python in %s failed: %v\n%s", container, err, out)
	}
	return string(out)
}

func interopConfig() Config {
	cfg := DefaultConfig()
	cfg.Queues = []string{interopQueue}
	cfg.QueueConcurrency = map[string]int{interopQueue: 1}
	cfg.PopTimeout = time.Second
	cfg.RestoreInterval = time.Hour // the tests drive the scan themselves
	cfg.RenewInterval = time.Hour
	cfg.ShutdownGrace = 2 * time.Second
	cfg.Name = "go-worker-interop"
	return cfg
}

// Half of "only the consumer moves": the Go worker reads what Celery's own
// client wrote, down to this app's enqueue stamp.
func TestAMessageFromCelerysOwnClientIsReadAndMeasured(t *testing.T) {
	rdb := testRedis(t)
	took := make(chan *celery.Message, 1)
	hold := make(chan struct{})
	consumer, _ := runConsumerOn(t, rdb, interopConfig(), runnerFunc(func(_ context.Context, msg *celery.Message) error {
		took <- msg
		<-hold
		return nil
	}))

	if out := runPython(t, celeryProducer); !strings.Contains(out, "sent") {
		t.Fatalf("the producer did not send: %s", out)
	}

	var msg *celery.Message
	select {
	case msg = <-took:
	case <-time.After(30 * time.Second):
		t.Fatal("the Celery-produced message was never taken")
	}

	if msg.Task != "probe.interop.task" {
		t.Errorf("Task = %q", msg.Task)
	}
	if len(msg.Args) != 2 || msg.Args[1] != "two" || msg.Kwargs["k"] != true {
		t.Errorf("Args/Kwargs = %#v / %#v", msg.Args, msg.Kwargs)
	}
	// Celery publishes a routed task through the anonymous exchange with the
	// queue as the routing key, even when the queue has a direct exchange of
	// its own. This is why a restore target is derived from the routing key:
	// kombu's own _lookup does the same for the anonymous exchange.
	if msg.Queue != interopQueue {
		t.Errorf("routing key = %q", msg.Queue)
	}
	if msg.Exchange != "" {
		t.Errorf("exchange = %q, want the anonymous exchange", msg.Exchange)
	}
	if msg.DeliveryTag == "" {
		t.Error("Celery's own producer should have stamped a delivery tag")
	}

	snapshot, err := consumer.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	wait := snapshot.Wait["probe.interop.task"]
	if wait.Count != 1 {
		t.Fatalf("queue wait was not measured from Celery's stamp: %+v (unknown: %d)", wait, snapshot.WaitUnknown)
	}
	if wait.Max <= 0 || wait.Max > time.Minute {
		t.Errorf("queue wait = %v; a stamp from another clock would look like this", wait.Max)
	}
	close(hold)
}

// The cutover question, from Python's side: can a Celery worker recover a
// message this worker was holding when it died? It can only do that if the
// claim is in the keys and the shape it expects.
func TestACeleryWorkerCanRestoreThisWorkersClaim(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	took := make(chan *celery.Message, 1)
	hold := make(chan struct{})
	defer close(hold)
	_, _ = runConsumerOn(t, rdb, interopConfig(), runnerFunc(func(_ context.Context, msg *celery.Message) error {
		took <- msg
		<-hold
		return nil
	}))

	runPython(t, celeryProducer)
	var msg *celery.Message
	select {
	case msg = <-took:
	case <-time.After(30 * time.Second):
		t.Fatal("the message was never taken")
	}
	if n, _ := rdb.HLen(ctx, "unacked").Result(); n != 1 {
		t.Fatalf("this worker holds %d claims, want 1", n)
	}

	out := runPython(t, kombuRestore)
	if !strings.Contains(out, "restored 1") {
		t.Fatalf("kombu did not restore this worker's claim: %s", out)
	}

	back, err := rdb.RPop(ctx, interopQueue).Result()
	if err != nil {
		t.Fatalf("the restored message should be on the queue: %v", err)
	}
	restored, err := celery.Decode([]byte(back))
	if err != nil {
		t.Fatalf("kombu restored something this worker cannot read: %v", err)
	}
	if restored.DeliveryTag != msg.DeliveryTag || restored.Task != msg.Task {
		t.Errorf("kombu restored %q/%q, not %q/%q", restored.Task, restored.DeliveryTag, msg.Task, msg.DeliveryTag)
	}
	if !restored.Redelivered {
		t.Error("kombu should have stamped the restored message redelivered")
	}
}

// The cutover question from this worker's side: a Python worker killed holding
// a message must be recoverable here, because during a gradual cutover this
// worker may be the only consumer left on the queue.
func TestThisWorkerCanRestoreACeleryWorkersClaim(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()

	runPython(t, celeryProducer)
	if out := runPython(t, kombuTakeAndDie); !strings.Contains(out, "took") || strings.Contains(out, "nothing") {
		t.Fatalf("the Python consumer took nothing: %s", out)
	}
	held, err := rdb.HKeys(ctx, "unacked").Result()
	if err != nil || len(held) != 1 {
		t.Fatalf("Python left %q in unacked, want one claim (%v)", held, err)
	}
	if n, _ := rdb.LLen(ctx, interopQueue).Result(); n != 0 {
		t.Fatal("the message should be off the queue and held only by the claim")
	}

	// A window of zero makes every claim stale, which is what a long-dead
	// worker's claim looks like.
	registry := NewRegistry(rdb, CeleryRegistryKeys(), 0)
	restored, err := registry.RestoreVisible(ctx)
	if err != nil {
		t.Fatalf("RestoreVisible: %v", err)
	}
	if restored != 1 {
		t.Fatalf("restored %d of Python's claims, want 1", restored)
	}

	back, err := rdb.RPop(ctx, interopQueue).Result()
	if err != nil {
		t.Fatalf("the restored message should be on the queue: %v", err)
	}
	msg, err := celery.Decode([]byte(back))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if msg.Task != "probe.interop.task" || !msg.Redelivered {
		t.Errorf("restored %q, redelivered %v", msg.Task, msg.Redelivered)
	}
	if held, _ := rdb.HLen(ctx, "unacked").Result(); held != 0 {
		t.Error("the restored claim should be gone")
	}
}

// restoreKey derives a message's destination from its routing key instead of
// walking kombu's binding table. This is the check that the two agree.
func TestTheBindingTableAgreesWithTheDerivedRestoreKey(t *testing.T) {
	_ = testRedis(t)
	out := strings.TrimSpace(runPython(t, kombuBindings))
	bound := strings.Split(out, "|")
	if len(bound) != 1 || bound[0] != interopQueue {
		t.Fatalf("kombu binds routing key %q to %q", interopQueue, bound)
	}
	key, err := restoreKey(interopQueue, 0)
	if err != nil || key != bound[0] {
		t.Errorf("restoreKey = %q, %v; kombu would restore to %q", key, err, bound[0])
	}
}

// A Celery envelope captured here is worth keeping readable: this is the shape
// phase 3 will have to run.
func TestCelerysEnvelopeStaysTheShapePhaseOneCaptured(t *testing.T) {
	rdb := testRedis(t)
	runPython(t, celeryProducer)
	raw, err := rdb.RPop(context.Background(), interopQueue).Result()
	if err != nil {
		t.Fatalf("RPop: %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	for _, field := range []string{"body", "content-encoding", "content-type", "headers", "properties"} {
		if _, present := envelope[field]; !present {
			t.Errorf("the envelope has no %q", field)
		}
	}
	msg, err := celery.Decode([]byte(raw))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if msg.EnqueuedAt.IsZero() {
		t.Error("the enqueue stamp is the only source of queue wait and must be present")
	}
}
