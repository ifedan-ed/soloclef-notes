package livepublish

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testredis"
)

// What the worker publishes on its broker connection is heard by a
// subscriber on another database number of the same server, which is how
// the API listens (its URL names the application's database). Skipped
// without the throwaway server; nothing is written, so no database number
// of testredis's is taken.
func TestAChangeIsHeardWhicheverDatabaseTheListenerNames(t *testing.T) {
	address := os.Getenv(testredis.Variable)
	if address == "" {
		t.Skipf("%s is not set", testredis.Variable)
	}
	worker, listener := connect(t, address, 7), connect(t, address, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	subscription := listener.Subscribe(ctx, livechange.Channel)
	defer subscription.Close()
	if _, err := subscription.Receive(ctx); err != nil {
		t.Fatal(err)
	}

	New(worker, slog.New(slog.NewTextHandler(io.Discard, nil))).Publish(ctx, "u1",
		livechange.Change{Kind: livechange.Documents, ID: 12})

	message, err := subscription.ReceiveMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := livechange.Decode([]byte(message.Payload))
	if err != nil || decoded.UserID != "u1" || decoded.Changes[0] != (livechange.Change{Kind: livechange.Documents, ID: 12}) {
		t.Fatalf("heard %+v (%v)", decoded, err)
	}
}

func connect(t *testing.T, address string, database int) *redis.Client {
	t.Helper()
	options, err := redis.ParseURL(address)
	if err != nil {
		t.Fatal(err)
	}
	options.DB = database
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	return client
}
