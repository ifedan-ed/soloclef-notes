package dedupe

import (
	"context"
	"time"
)

// Keys is the four Redis commands the two guards are kept with. A tier that
// queues mail satisfies it with its own client, as it satisfies
// enqueue.Broker, so nothing here carries a Redis dependency.
//
// SetIfAbsent is SET key value NX GET EX ttl: it takes the key and tells the
// caller what was already there in one operation, so two racing senders cannot
// both be told they are first. Redis 7 and later; the queue's Redis is 8.
type Keys interface {
	// SetIfAbsent returns the value already held, or "" when it took the key.
	SetIfAbsent(ctx context.Context, key, value string, ttl time.Duration) (string, error)
	// Set writes whatever is there now.
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	// Get returns "" for a key that is not there.
	Get(ctx context.Context, key string) (string, error)
	Delete(ctx context.Context, key string) error
}
