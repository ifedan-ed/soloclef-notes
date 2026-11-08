package executor

import (
	"context"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

// The three variables the end-to-end test needs. None is defaulted: this test
// flushes the database it is given and starts real task processes, so it never
// runs by accident.
const (
	// EnvTestRedis is a throwaway Redis, as internal/broker's tests use
	// (soloclef_worker/AGENTS.md).
	EnvTestRedis = "SOLOCLEF_WORKER_TEST_REDIS"

	// EnvPythonContainer is a container running *this checkout's*
	// soloclef_backend, which the live backend container is not: the task
	// entrypoint is new here. The worker test starts task processes in it.
	EnvPythonContainer = "SOLOCLEF_WORKER_TEST_PYTHON_CONTAINER"

	// EnvTestRedisFromPython is the throwaway Redis as that container sees it.
	EnvTestRedisFromPython = "SOLOCLEF_WORKER_TEST_REDIS_FROM_PYTHON"
)

// liveQueues are this app's real queues. Finding one means the URL points at
// the live broker, whose work belongs to a running Celery worker.
var liveQueues = []string{"surfsense", "surfsense.connectors", "surfsense.heavy"}

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv(EnvTestRedis)
	if url == "" {
		t.Skipf("set %s to a throwaway Redis to run the end-to-end test", EnvTestRedis)
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
		t.Fatalf("%s holds a live queue; refusing to consume from it", EnvTestRedis)
	}
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flushing the test database: %v", err)
	}
	return rdb
}
