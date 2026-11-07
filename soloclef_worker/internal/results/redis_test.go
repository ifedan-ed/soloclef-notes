package results

import (
	"context"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

// EnvTestRedis names a throwaway Redis, the same variable internal/broker's
// tests use (soloclef_worker/AGENTS.md). It is never defaulted: these tests
// flush the database they are given.
const EnvTestRedis = "SOLOCLEF_WORKER_TEST_REDIS"

// liveQueues are this app's real queues. Finding one means the URL points at
// the live broker, whose result rows belong to running clients.
var liveQueues = []string{"surfsense", "surfsense.connectors", "surfsense.heavy"}

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv(EnvTestRedis)
	if url == "" {
		t.Skipf("set %s to a throwaway Redis to run the result backend's integration tests", EnvTestRedis)
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("%s: %v", EnvTestRedis, err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("%s is unreachable: %v", EnvTestRedis, err)
	}
	if n, err := rdb.Exists(ctx, liveQueues...).Result(); err == nil && n > 0 {
		t.Fatalf("%s holds a live queue; refusing to write result rows into it", EnvTestRedis)
	}
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flushing the test database: %v", err)
	}
	return rdb
}
