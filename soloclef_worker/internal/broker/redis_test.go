package broker

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testredis"
)

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	// A database of this package's own, so that emptying it cannot take a
	// message from a package tested at the same time (internal/testredis).
	return testredis.Open(t, testredis.Broker)
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}
