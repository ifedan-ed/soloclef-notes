package ratelimit

import (
	"context"
	"sync"
	"time"
)

// MemoryCounter is SlowAPI's in_memory_fallback: the counter used while the
// shared one cannot be reached. It counts within this process only, so two
// replicas falling back at once allow more than one budget — which is also true
// of the Python tier's workers, and is the price of answering at all while
// Redis is down.
//
// It mirrors limits.storage.MemoryStorage's fixed window: the count expires
// lazily on the next read of the same key, and the window starts at the first
// hit.
type MemoryCounter struct {
	// Now is overridable for tests only.
	Now func() time.Time

	mutex   sync.Mutex
	counts  map[string]int64
	expires map[string]time.Time
}

func (c *MemoryCounter) Hit(_ context.Context, key string, window time.Duration, cost int64) (int64, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.counts == nil {
		c.counts, c.expires = map[string]int64{}, map[string]time.Time{}
	}

	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	if expiry, counted := c.expires[key]; counted && !expiry.After(now()) {
		delete(c.counts, key)
		delete(c.expires, key)
	}

	c.counts[key] += cost
	if c.counts[key] == cost {
		c.expires[key] = now().Add(window)
	}
	return c.counts[key], nil
}
