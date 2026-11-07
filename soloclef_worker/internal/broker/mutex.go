package broker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// mutex is the lock kombu takes before scanning for stale claims, built the
// way redis-py's own lock is: SET NX with an expiry and a token, released only
// by the holder. Holding the same key by the same name is what stops this
// worker and a Python worker scanning at the same moment.
type mutex struct {
	rdb    redis.UniversalClient
	key    string
	expiry time.Duration
}

// acquire never blocks. A lock held elsewhere means another worker is already
// scanning, which is not a failure.
func (m mutex) acquire(ctx context.Context) (token string, held bool, err error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", false, fmt.Errorf("broker: lock token: %w", err)
	}
	token = hex.EncodeToString(raw)
	held, err = m.rdb.SetNX(ctx, m.key, token, m.expiry).Result()
	if err != nil {
		return "", false, fmt.Errorf("broker: taking the lock %s: %w", m.key, err)
	}
	return token, held, nil
}

// releaseScript deletes the lock only if this holder still owns it, so a scan
// that overran its expiry cannot delete the next worker's lock.
var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

func (m mutex) release(ctx context.Context, token string) error {
	if err := releaseScript.Run(ctx, m.rdb, []string{m.key}, token).Err(); err != nil {
		return fmt.Errorf("broker: releasing the lock %s: %w", m.key, err)
	}
	return nil
}
