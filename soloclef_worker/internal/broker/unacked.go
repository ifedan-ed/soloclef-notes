package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"github.com/redis/go-redis/v9"
)

// Redis has no broker ack, so task_acks_late cannot mean what it means on
// AMQP. Celery emulates it: a message taken off the list is recorded in an
// `unacked` hash and scored in an `unacked_index` sorted set, and a worker
// that scans the index finds entries older than the visibility window and
// pushes them back on their queue. Acknowledging is deleting the record.
//
// This worker writes those same two keys, with the same names and the same
// value shape, so a Python Celery worker on the same queue sees this worker's
// in-flight messages and can restore them, and this worker can restore a
// Python worker's. That symmetry is what makes a per-queue cutover reversible.
//
// One difference, and it is a fix rather than a divergence: this worker
// refreshes the score of a message it is still working on (Renew). Celery does
// not, so on this host — where the window is kombu's one-hour default and
// task_time_limit is eight hours — a Python worker's own long job can be
// restored under it and run twice. A refreshed claim cannot be.
type Registry struct {
	rdb    redis.UniversalClient
	keys   RegistryKeys
	window time.Duration
	now    func() time.Time
}

// RegistryKeys are the three Redis keys the ack emulation lives in.
type RegistryKeys struct {
	// Unacked is the hash of in-flight payloads by delivery tag.
	Unacked string
	// Index is the sorted set scoring each tag by when its claim was last
	// confirmed.
	Index string
	// Mutex serialises the scan for stale claims across workers.
	Mutex string
	// Scheduled is the sorted set of messages taken before they were due,
	// scored by their eta (scheduled.go). It is this worker's and not
	// kombu's: a Python worker holds such a message in its own memory and
	// never reads this set, and nothing it does writes to it.
	Scheduled string
}

// CeleryRegistryKeys are the names kombu's Redis transport uses, unprefixed,
// as this app leaves them (app/celery_app.py sets no broker_transport_options),
// and the name of the Go workers' own scheduled set beside them.
func CeleryRegistryKeys() RegistryKeys {
	return RegistryKeys{Unacked: "unacked", Index: "unacked_index", Mutex: "unacked_mutex", Scheduled: "soloclef_scheduled"}
}

// MutexExpiry is kombu's unacked_mutex_expire: how long a restore scan may
// hold the lock before another worker may assume it died holding it.
const MutexExpiry = 300 * time.Second

// NewRegistry opens the ack emulation for one worker.
func NewRegistry(rdb redis.UniversalClient, keys RegistryKeys, window time.Duration) *Registry {
	return &Registry{rdb: rdb, keys: keys, window: window, now: time.Now}
}

// Claim records a message as in flight. Until it is acked, requeued or
// restored, this is the only record that the message exists at all: BRPOP has
// already removed it from its list.
func (r *Registry) Claim(ctx context.Context, msg *celery.Message) error {
	if msg.DeliveryTag == "" {
		return errors.New("broker: message has no delivery tag to claim it by")
	}
	entry, err := json.Marshal([]any{json.RawMessage(msg.Raw), msg.Exchange, msg.Queue})
	if err != nil {
		return fmt.Errorf("broker: encoding the unacked entry: %w", err)
	}
	pipe := r.rdb.Pipeline()
	pipe.ZAdd(ctx, r.keys.Index, redisMember(msg.DeliveryTag, r.score(r.now())))
	pipe.HSet(ctx, r.keys.Unacked, msg.DeliveryTag, string(entry))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("broker: claiming %s: %w", msg.DeliveryTag, err)
	}
	return nil
}

// Renew confirms that the tags are still being worked on.
func (r *Registry) Renew(ctx context.Context, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	members := make([]redis.Z, 0, len(tags))
	score := r.score(r.now())
	for _, tag := range tags {
		members = append(members, redis.Z{Score: score, Member: tag})
	}
	if err := r.rdb.ZAdd(ctx, r.keys.Index, members...).Err(); err != nil {
		return fmt.Errorf("broker: renewing %d claims: %w", len(tags), err)
	}
	return nil
}

// Ack drops the claim, which on this transport is the acknowledgement itself.
// Under acks_late it happens once the job has finished, success or failure.
func (r *Registry) Ack(ctx context.Context, tag string) error {
	pipe := r.rdb.Pipeline()
	pipe.ZRem(ctx, r.keys.Index, tag)
	pipe.HDel(ctx, r.keys.Unacked, tag)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("broker: acking %s: %w", tag, err)
	}
	return nil
}

// Requeue gives a claimed message back without running it, at the producer end
// of its list so it waits behind everything already queued. This is what a
// message deferred by a full task class or abandoned at shutdown does, and it
// matches Celery's reject(requeue=True).
//
// It reports false when the claim was already gone, which means another worker
// restored it first: the caller must not then treat the message as its own.
func (r *Registry) Requeue(ctx context.Context, msg *celery.Message) (bool, error) {
	destination, err := restoreKey(msg.Queue, msg.Priority)
	if err != nil {
		return false, err
	}
	return r.restore(ctx, msg.DeliveryTag, msg.Raw, destination, queueBack)
}

// RestoreVisible is the half of acks_late that only a broker could otherwise
// do: it finds claims nobody has confirmed within the visibility window —
// the fingerprint of a worker that died mid-job — and makes those messages
// visible again at the consume end of their list.
//
// It returns how many it restored.
func (r *Registry) RestoreVisible(ctx context.Context) (int, error) {
	lock := mutex{rdb: r.rdb, key: r.keys.Mutex, expiry: MutexExpiry}
	token, held, err := lock.acquire(ctx)
	if err != nil {
		return 0, err
	}
	if !held {
		// Another worker is scanning. Kombu does nothing here too.
		return 0, nil
	}
	defer func() { _ = lock.release(ctx, token) }()

	ceiling := r.score(r.now().Add(-r.window))
	stale, err := r.rdb.ZRevRangeByScore(ctx, r.keys.Index, &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%f", ceiling),
	}).Result()
	if err != nil {
		return 0, fmt.Errorf("broker: scanning for stale claims: %w", err)
	}

	restored := 0
	for _, tag := range stale {
		won, err := r.restoreByTag(ctx, tag)
		if err != nil {
			return restored, err
		}
		if won {
			restored++
		}
	}
	return restored, nil
}

func (r *Registry) restoreByTag(ctx context.Context, tag string) (bool, error) {
	stored, err := r.rdb.HGet(ctx, r.keys.Unacked, tag).Result()
	if errors.Is(err, redis.Nil) {
		// Scored but no payload: drop the orphaned score rather than leave it
		// to be rescanned forever.
		return false, r.rdb.ZRem(ctx, r.keys.Index, tag).Err()
	}
	if err != nil {
		return false, fmt.Errorf("broker: reading claim %s: %w", tag, err)
	}

	payload, routingKey, err := splitEntry([]byte(stored))
	if err != nil {
		return false, err
	}
	priority, err := payloadPriority(payload)
	if err != nil {
		return false, err
	}
	destination, err := restoreKey(routingKey, priority)
	if err != nil {
		return false, err
	}
	return r.restore(ctx, tag, payload, destination, queueFront)
}

// queueFront and queueBack name the two ends of a Redis list in the terms that
// matter: BRPOP takes from the right, so RPUSH makes a message the next one
// out and LPUSH puts it behind everything already queued.
const (
	queueFront = "front"
	queueBack  = "back"
)

// restoreScript claims and restores in one step. The HDEL result is the claim:
// if this worker did not remove the entry, another worker already restored the
// message and must not have a duplicate pushed behind it.
var restoreScript = redis.NewScript(`
local claimed = redis.call('HDEL', KEYS[1], ARGV[1])
redis.call('ZREM', KEYS[2], ARGV[1])
if claimed == 0 then
  return 0
end
if ARGV[3] == 'back' then
  redis.call('LPUSH', KEYS[3], ARGV[2])
else
  redis.call('RPUSH', KEYS[3], ARGV[2])
end
return 1
`)

func (r *Registry) restore(ctx context.Context, tag string, payload []byte, destination, end string) (bool, error) {
	marked, err := markRedelivered(payload)
	if err != nil {
		return false, err
	}
	won, err := restoreScript.Run(ctx, r.rdb,
		[]string{r.keys.Unacked, r.keys.Index, destination},
		tag, string(marked), end,
	).Int()
	if err != nil {
		return false, fmt.Errorf("broker: restoring %s to %s: %w", tag, destination, err)
	}
	return won == 1, nil
}

// score is a unix timestamp in seconds with a fraction, which is what Python's
// time() gives kombu and so what a Python worker compares against.
func (r *Registry) score(at time.Time) float64 {
	return float64(at.UnixNano()) / float64(time.Second)
}

func redisMember(tag string, score float64) redis.Z {
	return redis.Z{Score: score, Member: tag}
}

// splitEntry reads the [payload, exchange, routing_key] triple kombu stores.
func splitEntry(entry []byte) (payload []byte, routingKey string, err error) {
	var parts []json.RawMessage
	if err := json.Unmarshal(entry, &parts); err != nil {
		return nil, "", fmt.Errorf("broker: unacked entry is not a JSON array: %w", err)
	}
	if len(parts) != 3 {
		return nil, "", fmt.Errorf("broker: unacked entry has %d parts, want 3", len(parts))
	}
	if err := json.Unmarshal(parts[2], &routingKey); err != nil {
		return nil, "", fmt.Errorf("broker: unacked entry routing key: %w", err)
	}
	return parts[0], routingKey, nil
}

// payloadPriority reads which of a queue's priority lists a stored message
// belongs on.
func payloadPriority(payload []byte) (int, error) {
	var envelope struct {
		Properties struct {
			Priority int `json:"priority"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return 0, fmt.Errorf("broker: stored payload is not an envelope: %w", err)
	}
	return envelope.Properties.Priority, nil
}
