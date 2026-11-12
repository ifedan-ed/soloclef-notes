package jobreconcile

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// fencePrefix is the fence's keys: one a moment, each expiring by itself.
const fencePrefix = "soloclef:worker:reconcile-tick:"

// RedisFence is Fence in the broker's own Redis: where the ticks are, so a
// Redis that was emptied forgets the fences along with the ticks they
// guarded.
type RedisFence struct {
	Redis redis.UniversalClient
}

func fenceKey(moment time.Time) string {
	return fencePrefix + strconv.FormatInt(moment.Unix(), 10)
}

func (f RedisFence) Take(ctx context.Context, moment time.Time, hold time.Duration) (bool, error) {
	return f.Redis.SetNX(ctx, fenceKey(moment), "1", hold).Result()
}

func (f RedisFence) Release(ctx context.Context, moment time.Time) error {
	return f.Redis.Del(ctx, fenceKey(moment)).Err()
}

// RedisBroker is produce.Broker over the worker's Redis client.
type RedisBroker struct {
	Redis redis.UniversalClient
}

func (b RedisBroker) LPush(ctx context.Context, key, value string) error {
	return b.Redis.LPush(ctx, key, value).Err()
}
