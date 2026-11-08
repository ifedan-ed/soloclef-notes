package produce

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
)

// These are the tests that decide whether this producer may enqueue work a
// Python worker will run: they read the routing table out of
// app/celery_app.py itself, and compare every envelope this package publishes,
// byte for byte, with one Celery's own client published for the same call.
//
// They need a container with this app installed and a URL for a throwaway
// Redis as that container sees it. Neither is defaulted, so this never
// publishes to the live broker by accident.
const (
	EnvCeleryContainer = "SOLOCLEF_CELERY_TEST_CONTAINER"
	EnvTestBroker      = "SOLOCLEF_CELERY_TEST_BROKER"
)

// probeQueue is a queue of this test's own. A live queue name here would put
// probe tasks in front of the workers that serve the product.
const probeQueue = "probe.produce"

// assertTestBroker is the trap soloclef_worker/AGENTS.md names: Celery
// resolves CELERY_BROKER_URL from the environment ahead of its constructor
// argument, and inside the backend container that names the live broker. Every
// script below publishes only after this has passed.
const assertTestBroker = `
import os
from celery import Celery
_probe = Celery("probe", broker=os.environ["BROKER_URL"])
assert _probe.conf.broker_url == os.environ["BROKER_URL"], (
    "broker_url resolved to %r, not the test broker" % (_probe.conf.broker_url,))
for name in ("surfsense", "surfsense.connectors", "surfsense.heavy"):
    assert name not in os.environ["BROKER_URL"], "the test broker names a live queue"
`

// dumpRouting reads the routing this app would apply, out of the app that
// defines it, so a change to task_routes shows up here rather than as work
// queued where no worker reads.
const dumpRouting = `
import json
from app.celery_app import celery_app, CONNECTORS_QUEUE, HEAVY_QUEUE, HEAVY_TASKS

routes = celery_app.conf.task_routes or {}
print("ROUTING " + json.dumps({
    "default_queue": celery_app.conf.task_default_queue,
    "connectors_queue": CONNECTORS_QUEUE,
    "heavy_queue": HEAVY_QUEUE,
    "heavy_tasks": list(HEAVY_TASKS),
    "routes": {task: route.get("queue") for task, route in routes.items()},
}))
`

// publishWithCelery sends one message per shape below with Celery's own
// client, through the same producer settings app/celery_app.py configures, and
// prints the envelope the broker holds. The app is rebuilt here rather than
// imported, because importing it would pull in the live broker.
const publishWithCelery = `
import json, os, time
from celery import Celery
from celery.signals import before_task_publish
from kombu import Connection

# The app's own two publish receivers, in the order app/celery_app.py connects
# them: the log context is imported first, the stamp defined after.
CONTEXT = {}

@before_task_publish.connect
def _propagate_context(headers=None, **_kwargs):
    if headers is not None and CONTEXT:
        headers["soloclef_log_context"] = dict(CONTEXT)

@before_task_publish.connect
def _stamp_enqueue_time(headers=None, **_kwargs):
    if headers is not None:
        headers["surfsense.enqueued_at_ns"] = str(time.monotonic_ns())

QUEUE = "probe.produce"
app = Celery("soloclef", broker=os.environ["BROKER_URL"])
app.conf.update(
    task_serializer="json", accept_content=["json"], result_serializer="json",
    timezone="UTC", enable_utc=True,
    task_default_queue=QUEUE, task_default_exchange=QUEUE,
    task_default_routing_key=QUEUE,
)

CALLS = [
    ("one int argument", dict(args=[7])),
    ("the shapes a connector import passes", dict(args=[[11, 12], 3, "6f1d0a3e-0000-4000-8000-000000000001"])),
    ("a countdown, and a None among the arguments", dict(args=[12, "chat", None], countdown=4)),
    ("both time limits, which travel as one header", dict(args=[9], time_limit=600, soft_time_limit=540)),
    ("an expiry", dict(args=[], expires=30)),
    ("keyword arguments", dict(args=[[4, 5]], kwargs={"folder_subtree_ids": [6, 7]})),
    ("text that json.dumps escapes", dict(args=["caf\u00e9 \u2014 \"quoted\" and 'single'", True, 1.5])),
]

DELAY_CALLS = [
    ("a .delay of one argument", (7,)),
    ("a .delay of several arguments", ([11, 12], 3, "a-document")),
    ("a .delay of nothing at all", ()),
]

connection = Connection(os.environ["BROKER_URL"])
channel = connection.default_channel

def published(label):
    raw = channel.client.rpop(QUEUE)
    print("ENVELOPE " + json.dumps({"label": label, "envelope": raw.decode()}))

for label, options in CALLS:
    app.send_task("probe.produce.task", **options)
    published(label)

# .delay is not send_task: it packs its arguments into a tuple, which Celery
# reprs as one.
@app.task(name="probe.produce.task")
def probe(*args, **kwargs):
    return None

for label, args in DELAY_CALLS:
    probe.delay(*args)
    published(label)

# And a task published during a request carries that request's log context.
CONTEXT.update({
    "request_id": "req_1ea615454491",
    "user_id": "11111111-1111-4111-8111-111111111111",
    "workspace_id": "3",
})
probe.delay(7)
published("a .delay inside a request, with its log context")
`

func runPython(t *testing.T, script string) string {
	t.Helper()
	container := os.Getenv(EnvCeleryContainer)
	brokerURL := os.Getenv(EnvTestBroker)
	if container == "" || brokerURL == "" {
		t.Skipf("set %s and %s to run the Celery interop tests", EnvCeleryContainer, EnvTestBroker)
	}
	cmd := exec.Command("docker", "exec", "-i",
		"-e", "BROKER_URL="+brokerURL,
		"-e", "CELERY_BROKER_URL="+brokerURL,
		"-e", "CELERY_RESULT_BACKEND="+brokerURL,
		container, "python", "-")
	cmd.Stdin = strings.NewReader(assertTestBroker + script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python in %s failed: %v\n%s", container, err, out)
	}
	return string(out)
}

type pythonRouting struct {
	DefaultQueue    string            `json:"default_queue"`
	ConnectorsQueue string            `json:"connectors_queue"`
	HeavyQueue      string            `json:"heavy_queue"`
	HeavyTasks      []string          `json:"heavy_tasks"`
	Routes          map[string]string `json:"routes"`
}

// The table this package routes by has to be the table app/celery_app.py
// computes: every task it names, on the queue it names, and nothing extra.
func TestTheRoutingIsTheOneCeleryWouldApply(t *testing.T) {
	out := runPython(t, dumpRouting)
	_, encoded, found := strings.Cut(out, "ROUTING ")
	if !found {
		t.Fatalf("the dump printed nothing usable:\n%s", out)
	}
	var python pythonRouting
	if err := json.Unmarshal([]byte(strings.TrimSpace(encoded)), &python); err != nil {
		t.Fatalf("the dump is not JSON: %v\n%s", err, encoded)
	}

	table := routing.Table{Default: python.DefaultQueue}
	if table.ConnectorsQueue() != python.ConnectorsQueue {
		t.Errorf("connectors queue %q here, %q there", table.ConnectorsQueue(), python.ConnectorsQueue)
	}
	if table.HeavyQueue() != python.HeavyQueue {
		t.Errorf("heavy queue %q here, %q there", table.HeavyQueue(), python.HeavyQueue)
	}
	// Every HEAVY_TASK is routed to the heavy queue by the comprehension in
	// task_routes, so a task added to that tuple and not to this one would be
	// published to the fast queue.
	for _, task := range python.HeavyTasks {
		if got := table.QueueFor(task); got != python.HeavyQueue {
			t.Errorf("%s: this table sends it to %q, Celery to %q", task, got, python.HeavyQueue)
		}
	}
	for task, queue := range python.Routes {
		if got := table.QueueFor(task); got != queue {
			t.Errorf("%s: this table sends it to %q, Celery to %q", task, got, queue)
		}
	}
	// And nothing this table routes is a task Celery leaves on the default
	// queue: a fast task sent to the heavy queue waits behind a podcast.
	for _, task := range append(append([]string{}, routing.Heavy...), routing.Connectors...) {
		if _, named := python.Routes[task]; !named {
			t.Errorf("this table routes %q and task_routes does not name it", task)
		}
	}
}

// dumpIsoformats asks Python how it spells the times Celery puts in the eta
// and expires headers. A countdown's eta is a wall-clock reading, so whether
// the spelling agrees cannot be left to whatever microsecond a test run lands
// on.
const dumpIsoformats = `
import json
from datetime import datetime, timezone

MICROSECONDS = [0, 1, 500000, 123450, 123400, 999999, 10, 100000]
print("ISOFORMATS " + json.dumps({
    str(micro): datetime(2026, 11, 8, 4, 30, 5, micro, tzinfo=timezone.utc).isoformat()
    for micro in MICROSECONDS
}))
`

// Python's isoformat writes all six microsecond digits or none at all, and
// Go's own ".999999" verb drops the trailing zeros. Roughly one wall-clock
// reading in ten ends in a zero, so this is the spelling of about a tenth of
// every eta a countdown publishes.
func TestAnETAIsSpelledTheWayPythonSpellsIt(t *testing.T) {
	out := runPython(t, dumpIsoformats)
	_, encoded, found := strings.Cut(out, "ISOFORMATS ")
	if !found {
		t.Fatalf("the dump printed nothing usable:\n%s", out)
	}
	var python map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(encoded)), &python); err != nil {
		t.Fatalf("the dump is not JSON: %v", err)
	}
	if len(python) == 0 {
		t.Fatal("no spellings to compare")
	}

	for micro, want := range python {
		microseconds, err := strconv.Atoi(micro)
		if err != nil {
			t.Fatalf("microsecond %q: %v", micro, err)
		}
		broker := &recordingBroker{}
		producer := Producer{
			Broker:  broker,
			Routes:  routing.Table{Default: probeQueue},
			Origin:  "gen1@test",
			ReplyTo: "00000000-0000-4000-8000-00000000ffff",
			Now: func() time.Time {
				return time.Date(2026, 11, 8, 4, 30, 5, microseconds*1000, time.UTC).
					Add(-time.Second)
			},
			Stamp: func() (time.Duration, error) { return 1, nil },
		}
		if _, err := producer.Send(context.Background(), Message{
			Task: "probe.produce.task", Countdown: time.Second,
		}); err != nil {
			t.Fatalf("Send: %v", err)
		}
		var envelope struct {
			Headers struct {
				ETA string `json:"eta"`
			} `json:"headers"`
		}
		if err := json.Unmarshal([]byte(broker.pushes[0].value), &envelope); err != nil {
			t.Fatalf("not an envelope: %v", err)
		}
		if envelope.Headers.ETA != want {
			t.Errorf("microsecond %s: this producer writes %q, Python writes %q",
				micro, envelope.Headers.ETA, want)
		}
	}
}

type publishedEnvelope struct {
	Label    string `json:"label"`
	Envelope string `json:"envelope"`
}

// The byte comparison, and nothing is blanked: the ids, the publisher and the
// two clocks are read out of Celery's own envelope and handed to this producer
// as the values it would have generated, so everything left is spelling.
//
// This is the test that found two divergences. Python's isoformat writes all
// six microsecond digits and Go's own format verb drops the trailing zeros, so
// one eta in ten was spelled differently; and Celery writes the timelimit
// header hard limit first, which this encoder wrote the other way round.
func TestEveryEnvelopeIsByteForByteWhatCeleryPublishes(t *testing.T) {
	out := runPython(t, publishWithCelery)

	shapes := map[string]Message{
		"one int argument": {Args: []any{7}},
		"the shapes a connector import passes": {
			Args: []any{[]any{11, 12}, 3, "6f1d0a3e-0000-4000-8000-000000000001"},
		},
		"a countdown, and a None among the arguments": {
			Args: []any{12, "chat", nil}, Countdown: 4 * time.Second,
		},
		"both time limits, which travel as one header": {
			Args: []any{9}, HardTimeLimit: 600 * time.Second, SoftTimeLimit: 540 * time.Second,
		},
		"an expiry": {ExpiresIn: 30 * time.Second},
		"keyword arguments": {
			Args:   []any{[]any{4, 5}},
			Kwargs: map[string]any{"folder_subtree_ids": []any{6, 7}},
		},
		"text that json.dumps escapes": {
			Args: []any{"café — \"quoted\" and 'single'", true, 1.5},
		},
		"a .delay of one argument":      Delay("", 7),
		"a .delay of several arguments": Delay("", []any{11, 12}, 3, "a-document"),
		"a .delay of nothing at all":    Delay(""),
		"a .delay inside a request, with its log context": func() Message {
			message := Delay("", 7)
			message.Context = LogContext{
				RequestID:   "req_1ea615454491",
				UserID:      "11111111-1111-4111-8111-111111111111",
				WorkspaceID: "3",
			}
			return message
		}(),
	}

	seen := 0
	for _, line := range strings.Split(out, "\n") {
		_, encoded, found := strings.Cut(line, "ENVELOPE ")
		if !found {
			continue
		}
		var published publishedEnvelope
		if err := json.Unmarshal([]byte(encoded), &published); err != nil {
			t.Fatalf("the publisher printed something unusable: %v\n%s", err, line)
		}
		shape, known := shapes[published.Label]
		if !known {
			t.Fatalf("Celery published a shape this test does not know: %q", published.Label)
		}
		seen++
		t.Run(published.Label, func(t *testing.T) {
			compareWithCelery(t, published.Envelope, shape)
		})
	}
	if seen != len(shapes) {
		t.Fatalf("compared %d envelopes of %d:\n%s", seen, len(shapes), out)
	}
}

// compareWithCelery publishes theirs again, here, and requires the same bytes.
func compareWithCelery(t *testing.T, theirs string, shape Message) {
	t.Helper()
	volatile := readVolatile(t, theirs)

	shape.Task = "probe.produce.task"
	shape.Queue = probeQueue

	broker := &recordingBroker{}
	ids := []string{volatile.id, volatile.deliveryTag}
	producer := Producer{
		Broker:  broker,
		Routes:  routing.Table{Default: probeQueue},
		Origin:  volatile.origin,
		ReplyTo: volatile.replyTo,
		NewID: func() (string, error) {
			next := ids[0]
			ids = ids[1:]
			return next, nil
		},
		// The instant Celery published at, recovered from the time it wrote:
		// an eta is now plus the countdown and an expiry is now plus the
		// window, so either one names the clock reading this test cannot see.
		Now:   func() time.Time { return volatile.publishedAt(t, shape) },
		Stamp: func() (time.Duration, error) { return volatile.stamp, nil },
	}
	if _, err := producer.Send(context.Background(), shape); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ours := broker.pushes[0].value
	if ours != theirs {
		t.Errorf("this producer wrote\n%s\nCelery wrote\n%s", ours, theirs)
	}
	if key := broker.pushes[0].key; key != probeQueue {
		t.Errorf("published to %q, want the queue's priority-0 list %q", key, probeQueue)
	}
}

type volatileValues struct {
	id          string
	deliveryTag string
	origin      string
	replyTo     string
	stamp       time.Duration
	eta         time.Time
	expires     time.Time
}

// publishedAt is the clock reading Celery took, worked back from whichever of
// the two times the call made it write. A call that made it write neither can
// be published at any instant, because no field carries one.
func (v volatileValues) publishedAt(t *testing.T, shape Message) time.Time {
	t.Helper()
	switch {
	case shape.Countdown > 0:
		if v.eta.IsZero() {
			t.Fatal("a countdown published with no eta on the message")
		}
		return v.eta.Add(-shape.Countdown)
	case shape.ExpiresIn > 0:
		if v.expires.IsZero() {
			t.Fatal("an expiry published with no expires header")
		}
		return v.expires.Add(-shape.ExpiresIn)
	default:
		return time.Time{}
	}
}

func readVolatile(t *testing.T, envelope string) volatileValues {
	t.Helper()
	msg, err := celery.Decode([]byte(envelope))
	if err != nil {
		t.Fatalf("Celery published something this decoder cannot read: %v\n%s", err, envelope)
	}
	var decoded struct {
		Headers struct {
			Origin     string `json:"origin"`
			EnqueuedAt string `json:"surfsense.enqueued_at_ns"`
		} `json:"headers"`
		Properties struct {
			ReplyTo string `json:"reply_to"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(envelope), &decoded); err != nil {
		t.Fatalf("not an envelope: %v", err)
	}
	stamp, err := strconv.ParseInt(decoded.Headers.EnqueuedAt, 10, 64)
	if err != nil {
		t.Fatalf("the enqueue stamp is %q: %v", decoded.Headers.EnqueuedAt, err)
	}
	for name, value := range map[string]string{
		"id": msg.ID, "delivery_tag": msg.DeliveryTag,
		"origin": decoded.Headers.Origin, "reply_to": decoded.Properties.ReplyTo,
	} {
		if value == "" {
			t.Fatalf("Celery's envelope has no %s:\n%s", name, envelope)
		}
	}
	return volatileValues{
		id:          msg.ID,
		deliveryTag: msg.DeliveryTag,
		origin:      decoded.Headers.Origin,
		replyTo:     decoded.Properties.ReplyTo,
		stamp:       time.Duration(stamp),
		eta:         msg.ETA,
		expires:     msg.Expires,
	}
}
