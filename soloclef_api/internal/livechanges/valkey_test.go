package livechanges

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
)

// The bus end to end over a real Valkey: a change published by one process
// (here, the same one) reaches the account's stream through the
// subscription, and nobody else's. SOLOCLEF_API_TEST_VALKEY_URL names a
// throwaway server; the test only publishes and subscribes on the bus's own
// channel, and is skipped without it.
func TestAChangePublishedOnTheBusReachesTheAccountsStream(t *testing.T) {
	address := os.Getenv("SOLOCLEF_API_TEST_VALKEY_URL")
	if address == "" {
		t.Skip("SOLOCLEF_API_TEST_VALKEY_URL is not set")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus, err := NewValkey(address)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	hub := NewHub(nil)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go bus.Listen(ctx, hub, logger)

	alice, _, _ := hub.Subscribe("alice", "")
	defer alice.Close()
	bob, _, _ := hub.Subscribe("bob", "")
	defer bob.Close()
	publisher := livechange.Bus{Send: bus.Send, Logger: logger}

	// The subscription is made in the background; publish until it hears.
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		publisher.Publish(ctx, "alice", livechange.Change{Kind: livechange.Outputs, NotebookID: 3})
		select {
		case frame := <-alice.Frames():
			if frame.Change != (livechange.Change{Kind: livechange.Outputs, NotebookID: 3}) {
				t.Fatalf("alice heard %+v", frame)
			}
			select {
			case frame := <-bob.Frames():
				t.Fatalf("bob heard alice's change: %+v", frame)
			default:
			}
			return
		case <-alice.Dropped():
			// The first subscription's gap ends the streams open before it.
			alice.Close()
			alice, _, _ = hub.Subscribe("alice", "")
			bob.Close()
			bob, _, _ = hub.Subscribe("bob", "")
		case <-deadline:
			t.Fatal("the change never arrived")
		case <-tick.C:
		}
	}
}
