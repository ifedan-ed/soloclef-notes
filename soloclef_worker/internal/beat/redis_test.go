package beat

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// EnvTestRedis names a throwaway Redis for the beat's integration tests. It is
// never defaulted: these tests flush the database they are pointed at, and a
// beat pointed at the live broker would publish real scheduled work.
const EnvTestRedis = "SOLOCLEF_WORKER_TEST_REDIS"

// liveQueues are the queues this app really uses. Finding one in the test
// database means the URL points somewhere this must not touch.
var liveQueues = []string{"surfsense", "surfsense.connectors", "surfsense.heavy"}

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv(EnvTestRedis)
	if url == "" {
		t.Skipf("set %s to a throwaway Redis to run the beat's integration tests", EnvTestRedis)
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
	for _, queue := range liveQueues {
		if n, err := rdb.Exists(ctx, queue).Result(); err == nil && n > 0 {
			t.Fatalf("%s holds the live queue %q; refusing to touch it", EnvTestRedis, queue)
		}
	}
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flushing the test database: %v", err)
	}
	return rdb
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}
