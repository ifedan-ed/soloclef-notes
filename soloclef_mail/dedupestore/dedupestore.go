// Package dedupestore keeps the dedupe keys in Redis: dedupe.Keys with
// go-redis behind it.
//
// It is its own package because it is the only thing in the dedupe half that
// needs a Redis client, and a tier that queues mail must not acquire one by
// importing enqueue -- it brings its own, as enqueue.Broker says.
package dedupestore

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Keys is the keyspace the two guards are kept in. One per process.
type Keys struct {
	Redis redis.Cmdable
}

// SetIfAbsent is SET key value NX GET EX ttl, which takes the key and reports
// what was already there in one operation. go-redis spells it SetArgs.
func (k Keys) SetIfAbsent(ctx context.Context, key, value string, ttl time.Duration) (string, error) {
	held, err := k.Redis.SetArgs(ctx, key, value, redis.SetArgs{
		Mode: "NX", Get: true, TTL: ttl,
	}).Result()
	if err == redis.Nil {
		// Nothing was there, so this call took the key.
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return held, nil
}

func (k Keys) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return k.Redis.Set(ctx, key, value, ttl).Err()
}

func (k Keys) Get(ctx context.Context, key string) (string, error) {
	value, err := k.Redis.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	return value, err
}

func (k Keys) Delete(ctx context.Context, key string) error {
	return k.Redis.Del(ctx, key).Err()
}
