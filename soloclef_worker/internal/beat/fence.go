package beat

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// fence makes a window's tick idempotent. The lease stops two beats running
// the schedule at all; the fence stops the one case the lease cannot cover —
// a lease changing hands inside a minute, so that both the beat that lost it
// and the beat that gained it believe the window is theirs.
//
// It is a claim rather than a lock: there is nothing to release, because what
// it records is that the window has happened.
type fence struct {
	rdb    redis.UniversalClient
	prefix string
	ttl    time.Duration
}

// claim reports whether this beat is the one that fires this entry's window.
func (f fence) claim(ctx context.Context, entry string, window time.Time) (bool, error) {
	key := f.key(entry, window)
	won, err := f.rdb.SetNX(ctx, key, "1", f.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("beat: claiming %s: %w", key, err)
	}
	return won, nil
}

// key names one entry's one window. The window is a unix minute, so two
// schedulers naming the same window agree on its name whatever their time
// zones are set to.
func (f fence) key(entry string, window time.Time) string {
	return f.prefix + ":fired:" + entry + ":" + strconv.FormatInt(window.UTC().Unix(), 10)
}
