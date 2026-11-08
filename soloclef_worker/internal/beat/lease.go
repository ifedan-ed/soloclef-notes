package beat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// lease is the single-scheduler lock, built the way redis-py's own lock is —
// SET NX with an expiry and a token, released only by its holder — because
// that is the shape the rest of this worker already locks with
// (broker/mutex.go, kombu's unacked_mutex).
//
// It carries an expiry because a beat that is SIGKILLed releases nothing: the
// schedule has to become free on its own or no successor could ever run it.
type lease struct {
	rdb   redis.UniversalClient
	key   string
	ttl   time.Duration
	owner string
	token string
}

// take asks for the schedule. Not getting it is the normal state of a second
// beat, not a failure.
func (l *lease) take(ctx context.Context) (bool, error) {
	token, err := leaseToken(l.owner)
	if err != nil {
		return false, err
	}
	held, err := l.rdb.SetNX(ctx, l.key, token, l.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("beat: taking the lease %s: %w", l.key, err)
	}
	if held {
		l.token = token
	}
	return held, nil
}

// renewScript extends the lease only while this holder still owns it, so a
// beat that lost the schedule — to a pause long enough for the TTL to pass —
// cannot take it back from whoever now has it.
var renewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
`)

// renew reports whether the lease is still this beat's.
func (l *lease) renew(ctx context.Context) (bool, error) {
	if l.token == "" {
		return false, nil
	}
	kept, err := renewScript.Run(ctx, l.rdb,
		[]string{l.key}, l.token, l.ttl.Milliseconds()).Int()
	if err != nil {
		return false, fmt.Errorf("beat: renewing the lease %s: %w", l.key, err)
	}
	if kept != 1 {
		l.token = ""
	}
	return kept == 1, nil
}

var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// release gives the schedule up at once, so a planned restart does not cost a
// whole TTL of silence.
func (l *lease) release(ctx context.Context) error {
	if l.token == "" {
		return nil
	}
	token := l.token
	l.token = ""
	if err := releaseScript.Run(ctx, l.rdb, []string{l.key}, token).Err(); err != nil {
		return fmt.Errorf("beat: releasing the lease %s: %w", l.key, err)
	}
	return nil
}

// held is whether this beat may publish. Nothing but the lease decides that.
func (l *lease) held() bool { return l.token != "" }

// leaseToken names the holder as well as being unique, so the key says which
// process has the schedule.
func leaseToken(owner string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("beat: lease token: %w", err)
	}
	return owner + ":" + hex.EncodeToString(raw), nil
}
