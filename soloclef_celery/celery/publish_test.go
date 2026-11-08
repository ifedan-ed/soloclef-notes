package celery

import (
	"encoding/json"
	"testing"
	"time"
)

// The captured envelope is the contract in both directions: phase 1 proved the
// decoder reads what Celery's client writes, and this proves the encoder
// writes it. Byte for byte, because a beat tick is published for a *Python*
// worker to consume and the only way to know the spelling is right is to
// spell it.
func TestEncodeReproducesTheCapturedLiveEnvelope(t *testing.T) {
	raw, err := Encode(Task{
		Name:         "probe.wire_format",
		ID:           "23fbf912-949d-4146-ad96-5911471d5da1",
		RootID:       "23fbf912-949d-4146-ad96-5911471d5da1",
		Args:         []any{1, "two"},
		Kwargs:       map[string]any{"k": true},
		RoutingKey:   "soloclef.goprobe",
		ReplyTo:      "d722a210-57e9-3b20-9662-35e3b7709447",
		Origin:       "gen463@3b13c9531d10",
		DeliveryTag:  "29c542e6-f1d5-4414-9e6e-438bb374efe8",
		EnqueuedAtNs: "1833747970624788",
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if string(raw) != liveEnvelope {
		t.Errorf("Encode wrote\n%s\nCelery wrote\n%s", raw, liveEnvelope)
	}
}

func TestEncodeRoundTripsThroughTheDecoder(t *testing.T) {
	due := time.Date(2026, 11, 8, 4, 30, 0, 0, time.UTC)
	raw, err := Encode(Task{
		Name:        "evict_etl_cache",
		ID:          "00000000-0000-4000-8000-000000000001",
		RootID:      "00000000-0000-4000-8000-000000000001",
		RoutingKey:  "surfsense",
		DeliveryTag: "00000000-0000-4000-8000-000000000002",
		Expires:     due.Add(10 * time.Minute),
		ETA:         due,
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	msg, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if msg.Task != "evict_etl_cache" || msg.Queue != "surfsense" {
		t.Errorf("task %q on %q", msg.Task, msg.Queue)
	}
	if len(msg.Args) != 0 || len(msg.Kwargs) != 0 {
		t.Errorf("args/kwargs = %#v / %#v, want empty", msg.Args, msg.Kwargs)
	}
	if !msg.ETA.Equal(due) || !msg.Expires.Equal(due.Add(10*time.Minute)) {
		t.Errorf("eta/expires = %v / %v", msg.ETA, msg.Expires)
	}
	if msg.DeliveryTag == "" {
		t.Error("a published message needs a delivery tag: it is the key of the unacked registry")
	}
}

// An eta or expires is written the way Python's datetime.isoformat writes one,
// because a Celery worker parses these headers with its own parser.
func TestEncodeWritesTimesAsPythonIsoformat(t *testing.T) {
	raw, err := Encode(Task{
		Name: "t", ID: "i", RootID: "i", RoutingKey: "q", DeliveryTag: "d",
		Expires: time.Date(2026, 11, 8, 4, 30, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var envelope struct {
		Headers struct {
			Expires string `json:"expires"`
		} `json:"headers"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if envelope.Headers.Expires != "2026-11-08T04:30:05+00:00" {
		t.Errorf("expires = %q", envelope.Headers.Expires)
	}
}

// Python's isoformat writes all six microsecond digits or none; Go's own
// ".999999" drops the trailing zeros. An eta comes from a wall clock, so a
// tenth of them end in one, and the two spellings parse the same and are not
// the same bytes.
func TestEncodeWritesAllSixMicrosecondDigits(t *testing.T) {
	cases := map[int]string{
		0:      "2026-11-08T04:30:05+00:00",
		500000: "2026-11-08T04:30:05.500000+00:00",
		123450: "2026-11-08T04:30:05.123450+00:00",
		1:      "2026-11-08T04:30:05.000001+00:00",
		999999: "2026-11-08T04:30:05.999999+00:00",
	}
	for micro, want := range cases {
		raw, err := Encode(Task{
			Name: "t", ID: "i", RootID: "i", RoutingKey: "q", DeliveryTag: "d",
			ETA: time.Date(2026, 11, 8, 4, 30, 5, micro*1000, time.UTC),
		})
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		var envelope struct {
			Headers struct {
				ETA string `json:"eta"`
			} `json:"headers"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("not JSON: %v", err)
		}
		if envelope.Headers.ETA != want {
			t.Errorf("microsecond %d wrote %q, want %q", micro, envelope.Headers.ETA, want)
		}
	}
}

func TestEncodeRefusesAMessageNothingCouldTrack(t *testing.T) {
	cases := map[string]Task{
		"no task name":    {ID: "i", RoutingKey: "q", DeliveryTag: "d"},
		"no id":           {Name: "t", RoutingKey: "q", DeliveryTag: "d"},
		"no routing key":  {Name: "t", ID: "i", DeliveryTag: "d"},
		"no delivery tag": {Name: "t", ID: "i", RoutingKey: "q"},
	}
	for name, task := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Encode(task); err == nil {
				t.Fatal("expected an error, got none")
			}
		})
	}
}
