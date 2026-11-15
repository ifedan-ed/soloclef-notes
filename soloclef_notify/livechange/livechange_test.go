package livechange

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	payload, err := Encode("u1", []Change{{Kind: Documents, ID: 12}, {Kind: Outputs, NotebookID: 7}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(payload), `{"user_id":"u1","changes":[{"type":"documents","id":12},{"type":"outputs","notebook_id":7}]}`; got != want {
		t.Fatalf("payload\n got %s\nwant %s", got, want)
	}
	message, err := Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if message.UserID != "u1" || len(message.Changes) != 2 || message.Changes[1].NotebookID != 7 {
		t.Fatalf("decoded %+v", message)
	}
}

func TestEncodeRefuses(t *testing.T) {
	cases := map[string]struct {
		user    string
		changes []Change
	}{
		"no owner":       {"", []Change{{Kind: Inbox}}},
		"no changes":     {"u1", nil},
		"unknown kind":   {"u1", []Change{{Kind: "rows"}}},
		"one bad of two": {"u1", []Change{{Kind: Inbox}, {Kind: "Inbox"}}},
	}
	for name, c := range cases {
		if _, err := Encode(c.user, c.changes); err == nil {
			t.Errorf("%s: encoded", name)
		}
	}
}

func TestDecodeDropsUnknownKindsAndRefusesNoOwner(t *testing.T) {
	message, err := Decode([]byte(`{"user_id":"u1","changes":[{"type":"future"},{"type":"inbox"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Changes) != 1 || message.Changes[0].Kind != Inbox {
		t.Fatalf("kept %+v", message.Changes)
	}
	if _, err := Decode([]byte(`{"changes":[{"type":"inbox"}]}`)); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("no owner: %v", err)
	}
	if _, err := Decode([]byte(`not json`)); err == nil {
		t.Fatal("decoded garbage")
	}
}

func TestBusSendsOnTheChannelAndOutlivesTheCaller(t *testing.T) {
	var gotChannel string
	var gotPayload []byte
	var gotErr error
	bus := Bus{
		Send: func(ctx context.Context, channel string, payload []byte) error {
			gotChannel, gotPayload, gotErr = channel, payload, ctx.Err()
			if _, ok := ctx.Deadline(); !ok {
				t.Error("a publish must be bounded")
			}
			return nil
		},
		Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	}
	// The request that wrote the row has already ended.
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	bus.Publish(ended, "u1", Change{Kind: Chats, ID: 5})
	if gotChannel != Channel || gotErr != nil || !strings.Contains(string(gotPayload), `"id":5`) {
		t.Fatalf("channel %q err %v payload %s", gotChannel, gotErr, gotPayload)
	}
}

func TestBusLogsAndSwallowsFailures(t *testing.T) {
	var log bytes.Buffer
	bus := Bus{
		Send:   func(context.Context, string, []byte) error { return errors.New("valkey down") },
		Logger: slog.New(slog.NewTextHandler(&log, nil)),
	}
	start := time.Now()
	bus.Publish(context.Background(), "u1", Change{Kind: Inbox})
	bus.Publish(context.Background(), "", Change{Kind: Inbox})
	if time.Since(start) > time.Second {
		t.Fatal("a failing publish held the caller")
	}
	if !strings.Contains(log.String(), "valkey down") || !strings.Contains(log.String(), "level=ERROR") {
		t.Fatalf("log: %s", log.String())
	}
}
