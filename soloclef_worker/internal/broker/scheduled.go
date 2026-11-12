package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"github.com/redis/go-redis/v9"
)

// A message with an eta (Celery's countdown=) that is taken before it is due
// waits in Redis, not in a worker. It moves out of its claim into a sorted
// set scored by its eta, and the consumers move it back to the consume end
// of its list once that time has come, where it is taken again and run.
//
// Why not hold it until it is due, as Celery does: a held message occupies a
// queue slot for the whole of its delay. A note's indexing is queued fifteen
// seconds ahead, so that a burst of autosaves is indexed once
// (soloclef_api/internal/indexqueue), and four notes being typed into would
// hold every slot of a four-slot index queue, with uploads waiting behind
// jobs that are doing nothing. Here a delay costs a slot nothing, and costs
// no memory in the worker either.
//
// Why not put it straight back on its list: with nothing else queued it
// would be taken again at once, a loop of pops for every second of its
// delay, and cooling the queue down to stop that would hold back the due
// messages behind it.
//
// Nothing is lost on the way. Each move is one script, so the message is in
// exactly one of its claim, the set and its list at every instant; and the
// script's ZREM or HDEL is the claim on the move, so two workers promoting at
// once cannot both push it.

// scheduleScript moves a claimed message into the scheduled set. The HDEL
// result is the claim, as in restoreScript: a claim that is already gone was
// restored by another worker, which now owns the message.
var scheduleScript = redis.NewScript(`
local claimed = redis.call('HDEL', KEYS[1], ARGV[1])
redis.call('ZREM', KEYS[2], ARGV[1])
if claimed == 0 then
  return 0
end
redis.call('ZADD', KEYS[3], ARGV[3], ARGV[2])
return 1
`)

// promoteScript moves one due member of the scheduled set to the consume end
// of its list. The ZREM result is the claim on the move.
var promoteScript = redis.NewScript(`
if redis.call('ZREM', KEYS[1], ARGV[1]) == 0 then
  return 0
end
redis.call('RPUSH', KEYS[2], ARGV[2])
return 1
`)

// promoteBatch bounds one read of the set. A burst of due messages is moved
// in several reads rather than one reply holding all of them.
const promoteBatch = 100

// Schedule moves a claimed message that is not due yet from its claim into
// the scheduled set, scored by its eta. It reports false when the claim was
// already gone: another worker restored the message and owns it now.
//
// The member is the same [payload, exchange, routing_key] triple a claim
// stores, so a promotion finds the message's list exactly as a restore does,
// and it is unique because the payload carries the delivery tag.
func (r *Registry) Schedule(ctx context.Context, msg *celery.Message) (bool, error) {
	if msg.DeliveryTag == "" {
		return false, errors.New("broker: message has no delivery tag to schedule it by")
	}
	if msg.ETA.IsZero() {
		return false, errors.New("broker: only a message with an eta can be scheduled")
	}
	entry, err := json.Marshal([]any{json.RawMessage(msg.Raw), msg.Exchange, msg.Queue})
	if err != nil {
		return false, fmt.Errorf("broker: encoding the scheduled entry: %w", err)
	}
	won, err := scheduleScript.Run(ctx, r.rdb,
		[]string{r.keys.Unacked, r.keys.Index, r.keys.Scheduled},
		msg.DeliveryTag, string(entry), strconv.FormatFloat(r.score(msg.ETA), 'f', 6, 64),
	).Int()
	if err != nil {
		return false, fmt.Errorf("broker: scheduling %s: %w", msg.DeliveryTag, err)
	}
	return won == 1, nil
}

// PromoteDue moves every scheduled message whose eta has come to the consume
// end of its list, the next out: it has waited its delay already, and
// putting it behind the queue would add the queue's wait to it. It returns
// how many it moved.
func (r *Registry) PromoteDue(ctx context.Context) (int, error) {
	moved := 0
	ceiling := strconv.FormatFloat(r.score(r.now()), 'f', 6, 64)
	for {
		due, err := r.rdb.ZRangeByScore(ctx, r.keys.Scheduled, &redis.ZRangeBy{
			Min: "-inf", Max: ceiling, Count: promoteBatch,
		}).Result()
		if err != nil {
			return moved, fmt.Errorf("broker: reading the scheduled messages: %w", err)
		}
		for _, member := range due {
			won, err := r.promote(ctx, member)
			if err != nil {
				return moved, err
			}
			if won {
				moved++
			}
		}
		if len(due) < promoteBatch {
			return moved, nil
		}
	}
}

func (r *Registry) promote(ctx context.Context, member string) (bool, error) {
	destination, payload, err := scheduledDestination(member)
	if err != nil {
		// Only Schedule writes this set, so a member that cannot be read
		// was not written by this code. It is removed, so that it is not
		// read again every second for ever, and the error says what it was.
		if remErr := r.rdb.ZRem(ctx, r.keys.Scheduled, member).Err(); remErr != nil {
			return false, fmt.Errorf("broker: an unreadable scheduled entry (%v) could not be removed: %w", err, remErr)
		}
		return false, fmt.Errorf("broker: dropped an unreadable scheduled entry: %w", err)
	}
	won, err := promoteScript.Run(ctx, r.rdb, []string{r.keys.Scheduled, destination}, member, string(payload)).Int()
	if err != nil {
		return false, fmt.Errorf("broker: promoting a scheduled message to %s: %w", destination, err)
	}
	return won == 1, nil
}

// scheduledDestination reads a member of the scheduled set: the list its
// message goes back to, and the message.
func scheduledDestination(member string) (string, []byte, error) {
	payload, routingKey, err := splitEntry([]byte(member))
	if err != nil {
		return "", nil, err
	}
	priority, err := payloadPriority(payload)
	if err != nil {
		return "", nil, err
	}
	destination, err := restoreKey(routingKey, priority)
	return destination, payload, err
}

// Scheduled is how many messages wait in the scheduled set, for every queue
// and every worker: the set is shared, as the lists are.
func (r *Registry) Scheduled(ctx context.Context) (int64, error) {
	count, err := r.rdb.ZCard(ctx, r.keys.Scheduled).Result()
	if err != nil {
		return 0, fmt.Errorf("broker: counting the scheduled messages: %w", err)
	}
	return count, nil
}

// promoteInterval is how often a consumer moves due messages back to their
// lists, and so how late after its eta a delayed message may be taken: at
// most this, plus the wait for a slot. A note's delay is fifteen seconds, and
// a second on top of it is nothing a person typing can notice; one sorted-set
// read a second per worker is nothing Redis can notice.
const promoteInterval = time.Second
