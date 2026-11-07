package celery

import (
	"testing"
	"time"
)

// Captured from this app's own Redis on 2026-11-07 by enqueuing through
// app.celery_app.send_task, so the parser is tested against what the running
// FastAPI container actually produces rather than against the spec.
const liveEnvelope = `{"body": "W1sxLCAidHdvIl0sIHsiayI6IHRydWV9LCB7ImNhbGxiYWNrcyI6IG51bGwsICJlcnJiYWNrcyI6IG51bGwsICJjaGFpbiI6IG51bGwsICJjaG9yZCI6IG51bGx9XQ==", "content-encoding": "utf-8", "content-type": "application/json", "headers": {"lang": "py", "task": "probe.wire_format", "id": "23fbf912-949d-4146-ad96-5911471d5da1", "shadow": null, "eta": null, "expires": null, "group": null, "group_index": null, "retries": 0, "timelimit": [null, null], "root_id": "23fbf912-949d-4146-ad96-5911471d5da1", "parent_id": null, "argsrepr": "[1, 'two']", "kwargsrepr": "{'k': True}", "origin": "gen463@3b13c9531d10", "ignore_result": false, "replaced_task_nesting": 0, "stamped_headers": null, "stamps": {}, "surfsense.enqueued_at_ns": "1833747970624788"}, "properties": {"correlation_id": "23fbf912-949d-4146-ad96-5911471d5da1", "reply_to": "d722a210-57e9-3b20-9662-35e3b7709447", "delivery_mode": 2, "delivery_info": {"exchange": "", "routing_key": "soloclef.goprobe"}, "priority": 0, "body_encoding": "base64", "delivery_tag": "29c542e6-f1d5-4414-9e6e-438bb374efe8"}}`

func TestDecodeLiveEnvelope(t *testing.T) {
	msg, err := Decode([]byte(liveEnvelope))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if msg.Task != "probe.wire_format" {
		t.Errorf("Task = %q", msg.Task)
	}
	if msg.ID != "23fbf912-949d-4146-ad96-5911471d5da1" {
		t.Errorf("ID = %q", msg.ID)
	}
	if msg.Queue != "soloclef.goprobe" {
		t.Errorf("Queue = %q", msg.Queue)
	}
	if len(msg.Args) != 2 || msg.Args[1] != "two" {
		t.Errorf("Args = %#v", msg.Args)
	}
	if msg.Kwargs["k"] != true {
		t.Errorf("Kwargs = %#v", msg.Kwargs)
	}
	if !msg.ETA.IsZero() || !msg.Expires.IsZero() {
		t.Errorf("eta/expires should be zero, got %v / %v", msg.ETA, msg.Expires)
	}
	if msg.SoftTimeLimit != 0 || msg.HardTimeLimit != 0 {
		t.Errorf("timelimit [null,null] should be zero, got %v / %v", msg.SoftTimeLimit, msg.HardTimeLimit)
	}
	if msg.EnqueuedAt.IsZero() {
		t.Error("EnqueuedAt should come from the surfsense.enqueued_at_ns header")
	}
	if string(msg.Raw) != liveEnvelope {
		t.Error("Raw must be the envelope verbatim, for requeueing without re-encoding")
	}
}

func TestDecodeRejectsWhatItCannotRun(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"not json", `{`},
		{"no task header", `{"body":"W10=","headers":{},"properties":{}}`},
		{"other content type", `{"body":"x","content-type":"application/x-python-serialize","headers":{"task":"t"},"properties":{}}`},
		{"body not base64", `{"body":"!!!","headers":{"task":"t"},"properties":{"body_encoding":"base64"}}`},
		{"unknown body encoding", `{"body":"e30=","headers":{"task":"t"},"properties":{"body_encoding":"msgpack"}}`},
		{"body not an array", `{"body":"e30=","headers":{"task":"t"},"properties":{"body_encoding":"base64"}}`},
		{"body wrong arity", `{"body":"W1tdXQ==","headers":{"task":"t"},"properties":{"body_encoding":"base64"}}`},
		{"bad eta", `{"body":"W1tdLHt9LHt9XQ==","headers":{"task":"t","eta":"not-a-time"},"properties":{"body_encoding":"base64"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode([]byte(tc.raw)); err == nil {
				t.Fatal("expected an error, got none")
			}
		})
	}
}

func TestDecodeAcceptsAPlainJSONBody(t *testing.T) {
	// kombu may publish the body unencoded; both shapes must parse.
	raw := `{"body":"[[1],{\"a\":2},{}]","content-type":"application/json","headers":{"task":"t","id":"1"},"properties":{}}`
	msg, err := Decode([]byte(raw))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(msg.Args) != 1 || msg.Kwargs["a"] != float64(2) {
		t.Errorf("args=%#v kwargs=%#v", msg.Args, msg.Kwargs)
	}
}

func TestDecodeReadsETAExpiresAndTimeLimits(t *testing.T) {
	raw := `{"body":"W1tdLHt9LHt9XQ==","content-type":"application/json",
	 "headers":{"task":"t","id":"1","eta":"2026-11-07T21:00:00.500000+00:00",
	 "expires":"2026-11-07T22:00:00+00:00","timelimit":[28200,28800]},
	 "properties":{"body_encoding":"base64"}}`
	msg, err := Decode([]byte(raw))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := msg.ETA.UTC().Format(time.RFC3339Nano); got != "2026-11-07T21:00:00.5Z" {
		t.Errorf("ETA = %s", got)
	}
	if msg.SoftTimeLimit != 28200*time.Second || msg.HardTimeLimit != 28800*time.Second {
		t.Errorf("limits = %v / %v", msg.SoftTimeLimit, msg.HardTimeLimit)
	}
}

func TestDueHoldsFutureWorkAndDiscardsExpired(t *testing.T) {
	now := time.Date(2026, 11, 7, 21, 0, 0, 0, time.UTC)

	eta := &Message{ETA: now.Add(time.Minute)}
	if due, expired := eta.Due(now); due || expired {
		t.Errorf("an eta a minute out should hold: due=%v expired=%v", due, expired)
	}
	if due, _ := eta.Due(now.Add(2 * time.Minute)); !due {
		t.Error("once the eta passes it should run")
	}

	gone := &Message{Expires: now.Add(-time.Second)}
	if due, expired := gone.Due(now); due || !expired {
		t.Errorf("past its expiry it must be discarded: due=%v expired=%v", due, expired)
	}

	// Expiry outranks a future eta: nothing is worth holding to run late.
	both := &Message{ETA: now.Add(time.Hour), Expires: now.Add(-time.Second)}
	if _, expired := both.Due(now); !expired {
		t.Error("expiry should win over a pending eta")
	}

	if due, expired := (&Message{}).Due(now); !due || expired {
		t.Error("a message with neither should run now")
	}
}

// The broker tier needs four more envelope fields that phase 1 did not read:
// the delivery tag is the key of Celery's Redis ack-emulation registry, the
// exchange and priority decide where a lost message is restored to, and
// redelivered says the broker already handed this message out once.
func TestDecodeReadsWhatTheAckRegistryNeeds(t *testing.T) {
	msg, err := Decode([]byte(liveEnvelope))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if msg.DeliveryTag != "29c542e6-f1d5-4414-9e6e-438bb374efe8" {
		t.Errorf("DeliveryTag = %q", msg.DeliveryTag)
	}
	if msg.Exchange != "" {
		t.Errorf("Exchange = %q, want the anonymous exchange", msg.Exchange)
	}
	if msg.Priority != 0 {
		t.Errorf("Priority = %d", msg.Priority)
	}
	if msg.Redelivered {
		t.Error("a first delivery must not look redelivered")
	}
}

func TestDecodeReadsARedeliveredMessage(t *testing.T) {
	raw := `{"body":"W1tdLHt9LHt9XQ==","headers":{"task":"t","redelivered":true},` +
		`"properties":{"body_encoding":"base64","priority":6,"delivery_tag":"tag-1",` +
		`"delivery_info":{"exchange":"surfsense.heavy","routing_key":"surfsense.heavy"}}}`
	msg, err := Decode([]byte(raw))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !msg.Redelivered {
		t.Error("Redelivered should follow the headers.redelivered flag kombu sets on restore")
	}
	if msg.Exchange != "surfsense.heavy" || msg.Priority != 6 || msg.DeliveryTag != "tag-1" {
		t.Errorf("Exchange/Priority/DeliveryTag = %q/%d/%q", msg.Exchange, msg.Priority, msg.DeliveryTag)
	}
}

func TestIgnoreResultIsReadFromTheHeader(t *testing.T) {
	msg, err := Decode([]byte(liveEnvelope))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if msg.IgnoreResult {
		t.Error("the live envelope says ignore_result is false")
	}

	ignored, err := Decode([]byte(`{"body":"W1tdLHt9LHt9XQ==","content-encoding":"utf-8",` +
		`"headers":{"task":"push_send","id":"1","ignore_result":true},` +
		`"properties":{"body_encoding":"base64"}}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !ignored.IgnoreResult {
		t.Error("a task declared ignore_result must say so, or this worker writes rows Celery would not")
	}
}
