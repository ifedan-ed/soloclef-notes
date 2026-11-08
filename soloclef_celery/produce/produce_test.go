package produce

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
)

// written remembers what was pushed where, which is the whole of what a
// producer does to Redis.
type written struct {
	key   string
	value string
}

type recordingBroker struct{ pushes []written }

func (b *recordingBroker) LPush(_ context.Context, key, value string) error {
	b.pushes = append(b.pushes, written{key: key, value: value})
	return nil
}

func testProducer(t *testing.T) (Producer, *recordingBroker) {
	t.Helper()
	broker := &recordingBroker{}
	ids := 0
	return Producer{
		Broker:  broker,
		Routes:  routing.Table{Default: "surfsense"},
		Origin:  "gen1@api",
		ReplyTo: "00000000-0000-4000-8000-00000000ffff",
		NewID: func() (string, error) {
			ids++
			return "00000000-0000-4000-8000-00000000000" + strconv.Itoa(ids), nil
		},
		Now:   func() time.Time { return time.Date(2026, 11, 8, 13, 47, 0, 0, time.UTC) },
		Stamp: func() (time.Duration, error) { return 1833747970624788, nil },
	}, broker
}

func sent(t *testing.T, broker *recordingBroker) (written, *celery.Message) {
	t.Helper()
	if len(broker.pushes) != 1 {
		t.Fatalf("pushed %d messages, want 1", len(broker.pushes))
	}
	push := broker.pushes[0]
	msg, err := celery.Decode([]byte(push.value))
	if err != nil {
		t.Fatalf("what was published is not a task message: %v", err)
	}
	return push, msg
}

// The id Send returns is the id .delay() returns, and it is the id the
// envelope carries: a caller that polls the result backend with it has to find
// the task it just published.
func TestSendReturnsTheIdItPublished(t *testing.T) {
	producer, broker := testProducer(t)
	id, err := producer.Send(context.Background(), Message{Task: "delete_document", Args: []any{7}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, msg := sent(t, broker)
	if msg.ID != id {
		t.Errorf("published %q and returned %q", msg.ID, id)
	}
	if msg.RootID != id {
		t.Errorf("root_id = %q, want the task's own id", msg.RootID)
	}
	if len(msg.Args) != 1 || msg.Args[0] != float64(7) {
		t.Errorf("args = %#v", msg.Args)
	}
}

// A task the routing table names goes on that queue, and the Redis key is
// kombu's priority list rather than the bare queue name.
func TestSendRoutesEveryTaskWhereTaskRoutesSendsIt(t *testing.T) {
	cases := map[string]struct{ queue, key string }{
		"delete_document":             {"surfsense", "surfsense"},
		"index_google_drive_files":    {"surfsense.connectors", "surfsense.connectors"},
		"generate_video_presentation": {"surfsense.heavy", "surfsense.heavy"},
	}
	for task, want := range cases {
		producer, broker := testProducer(t)
		if _, err := producer.Send(context.Background(), Message{Task: task}); err != nil {
			t.Fatalf("Send: %v", err)
		}
		push, msg := sent(t, broker)
		if msg.Queue != want.queue {
			t.Errorf("%s: routing key %q, want %q", task, msg.Queue, want.queue)
		}
		if push.key != want.key {
			t.Errorf("%s: pushed to %q, want %q", task, push.key, want.key)
		}
	}
}

// apply_async(queue=…) overrides the table, and a priority picks a different
// list.
func TestAnExplicitQueueAndPriorityPickTheList(t *testing.T) {
	producer, broker := testProducer(t)
	if _, err := producer.Send(context.Background(), Message{
		Task: "generate_video_presentation", Queue: "probe.queue", Priority: 6,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	push, msg := sent(t, broker)
	if push.key != "probe.queue\x06\x166" {
		t.Errorf("pushed to %q", push.key)
	}
	if msg.Priority != 6 {
		t.Errorf("priority = %d", msg.Priority)
	}
}

// countdown is genuinely in use (app/notebooks/autoname/task.py settles 4s,
// deliverable_job_tasks.py backs off to 60s), and Celery sends it as an eta of
// now plus the countdown rather than as a countdown of its own.
func TestACountdownBecomesAnETA(t *testing.T) {
	producer, broker := testProducer(t)
	if _, err := producer.Send(context.Background(), Message{
		Task: "autoname_notebook", Args: []any{12, "chat", nil}, Countdown: 4 * time.Second,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, msg := sent(t, broker)
	want := time.Date(2026, 11, 8, 13, 47, 4, 0, time.UTC)
	if !msg.ETA.Equal(want) {
		t.Errorf("eta = %v, want %v", msg.ETA, want)
	}
	if due, _ := msg.Due(want.Add(-time.Second)); due {
		t.Error("a message with an eta in the future is due")
	}
}

// expires is two fields from one number: the header a worker compares against
// and the expiration property beside it, in milliseconds, as a string.
func TestAnExpiryWritesBothFieldsCeleryWrites(t *testing.T) {
	producer, broker := testProducer(t)
	if _, err := producer.Send(context.Background(), Message{
		Task: "check_periodic_schedules", ExpiresIn: 30 * time.Second,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	push, msg := sent(t, broker)
	if want := time.Date(2026, 11, 8, 13, 47, 30, 0, time.UTC); !msg.Expires.Equal(want) {
		t.Errorf("expires = %v, want %v", msg.Expires, want)
	}
	var envelope struct {
		Properties struct {
			Expiration string `json:"expiration"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(push.value), &envelope); err != nil {
		t.Fatalf("not an envelope: %v", err)
	}
	if envelope.Properties.Expiration != "30000" {
		t.Errorf("expiration = %q, want the window in milliseconds", envelope.Properties.Expiration)
	}
}

// The stamp is what makes queue wait measurable at all: the broker records
// nothing itself (app/celery_app.py's before_task_publish).
func TestTheEnqueueStampIsOnTheMessage(t *testing.T) {
	producer, broker := testProducer(t)
	if _, err := producer.Send(context.Background(), Message{Task: "delete_document"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, msg := sent(t, broker)
	if msg.EnqueuedAt.IsZero() {
		t.Fatal("no enqueue stamp")
	}
	if got := msg.EnqueuedAt.UnixNano(); got != 1833747970624788 {
		t.Errorf("enqueued_at_ns = %d", got)
	}
}

// An unreadable clock leaves the header off. A stamp from any other clock
// would be subtracted from CLOCK_MONOTONIC by the consumer and reported as a
// queue wait of some decades.
func TestAnUnreadableClockLeavesTheStampOff(t *testing.T) {
	producer, broker := testProducer(t)
	producer.Stamp = func() (time.Duration, error) { return 0, errAnyClockFailure }
	if _, err := producer.Send(context.Background(), Message{Task: "delete_document"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	push, msg := sent(t, broker)
	if !msg.EnqueuedAt.IsZero() {
		t.Errorf("stamped anyway: %v", msg.EnqueuedAt)
	}
	if strings.Contains(push.value, "surfsense.enqueued_at_ns") {
		t.Error("the header is present with no reading in it")
	}
}

var errAnyClockFailure = errors.New("this clock cannot be read")

// A producer that cannot name itself, or a task with no name, or a countdown
// in the past: each would publish a message that is not the one Celery's
// client writes, so none of them publishes at all.
func TestSendRefusesWhatCeleryWouldNotHavePublished(t *testing.T) {
	good, _ := testProducer(t)
	cases := map[string]struct {
		producer Producer
		message  Message
	}{
		"no task name": {good, Message{}},
		"no origin": {
			Producer{Broker: good.Broker, Routes: good.Routes, ReplyTo: good.ReplyTo},
			Message{Task: "t"},
		},
		"no reply_to": {
			Producer{Broker: good.Broker, Routes: good.Routes, Origin: good.Origin},
			Message{Task: "t"},
		},
		"no default queue": {
			Producer{Broker: good.Broker, Origin: good.Origin, ReplyTo: good.ReplyTo},
			Message{Task: "t"},
		},
		"no broker": {
			Producer{Routes: good.Routes, Origin: good.Origin, ReplyTo: good.ReplyTo},
			Message{Task: "t"},
		},
		"a countdown in the past": {good, Message{Task: "t", Countdown: -time.Second}},
		"an expiry in the past":   {good, Message{Task: "t", ExpiresIn: -time.Second}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.producer.Send(context.Background(), c.message); err == nil {
				t.Fatal("expected an error, got none")
			}
		})
	}
}
