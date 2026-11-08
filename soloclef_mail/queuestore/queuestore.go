// Package queuestore is the mail queue in Redis: the moves between its keys,
// as app/mail_queue/queue_store.py makes them.
//
// Every move is the same operation Python makes, not an equivalent one. A
// message is taken and held in one BLMOVE, so a sender that dies mid-send
// loses nothing; a retry is written and the held copy dropped in one
// transaction; and the due retries are promoted by the same Lua script, so two
// senders promoting at once cannot promote the same message twice.
package queuestore

import (
	"context"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/queuekeys"
	"github.com/redis/go-redis/v9"
)

// promoteDue is queue_store.py's _PROMOTE_DUE, character for character: a
// different script is a different atomicity.
const promoteDue = `
local due = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, 500)
for _, raw in ipairs(due) do
  redis.call('ZREM', KEYS[1], raw)
  redis.call('LPUSH', KEYS[2], raw)
end
return #due
`

var promote = redis.NewScript(promoteDue)

// Store is the queue. One per process.
type Store struct {
	Redis redis.Cmdable
}

// Push is the producer's end of the list. It is here as well as in the
// enqueue package's Broker seam because the sender process pushes nothing and
// a tier that pushes brings its own client.
func (s Store) Push(ctx context.Context, raw string) error {
	return s.Redis.LPush(ctx, queuekeys.Ready, raw).Err()
}

// Take moves the oldest ready message onto this sender's held list and returns
// it, waiting up to wait for one to arrive.
func (s Store) Take(ctx context.Context, sender int, wait time.Duration) (string, bool, error) {
	raw, err := s.Redis.BLMove(ctx, queuekeys.Ready, queuekeys.Sending(sender), "RIGHT", "LEFT", wait).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return raw, true, nil
}

// Done drops the message from this sender's held list.
func (s Store) Done(ctx context.Context, sender int, raw string) error {
	return s.Redis.LRem(ctx, queuekeys.Sending(sender), 1, raw).Err()
}

// RetryLater writes the next try onto the retry set and drops the held copy,
// in one transaction: either the message is due again or it is still held, and
// never neither.
func (s Store) RetryLater(ctx context.Context, sender int, raw, next string, due float64) error {
	_, err := s.Redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.ZAdd(ctx, queuekeys.Retry, redis.Z{Score: due, Member: next})
		pipe.LRem(ctx, queuekeys.Sending(sender), 1, raw)
		return nil
	})
	return err
}

// PromoteDue puts every retry due by now back in line, a batch at a time, and
// returns how many moved.
func (s Store) PromoteDue(ctx context.Context, now float64) (int, error) {
	// The score goes up as Python's repr would have written it, because the
	// script compares it as a string: a Go float formatted in exponent
	// notation is not a score Redis reads the same way.
	moved, err := promote.Run(ctx, s.Redis,
		[]string{queuekeys.Retry, queuekeys.Ready},
		strconv.FormatFloat(now, 'f', 6, 64)).Int()
	if err != nil {
		return 0, err
	}
	return moved, nil
}

// RequeueHeld puts back everything a previous run's senders were holding.
//
// Only safe when no other copy is sending, which is why exactly one mail
// sender process runs. It is the reason a crash mid-send loses nothing, and
// also the reason such a message may go out twice -- see AGENTS.md, "What does
// not dedupe".
func (s Store) RequeueHeld(ctx context.Context) (int, error) {
	moved := 0
	var cursor uint64
	for {
		keys, next, err := s.Redis.Scan(ctx, cursor, queuekeys.SendingPattern, 100).Result()
		if err != nil {
			return moved, err
		}
		for _, key := range keys {
			for {
				_, err := s.Redis.LMove(ctx, key, queuekeys.Ready, "RIGHT", "RIGHT").Result()
				if err == redis.Nil {
					break
				}
				if err != nil {
					return moved, err
				}
				moved++
			}
		}
		if next == 0 {
			return moved, nil
		}
		cursor = next
	}
}

// Depth is what the queue is carrying, for the line the service logs at start.
type Depth struct {
	Ready int64
	Retry int64
}

func (s Store) Depth(ctx context.Context) (Depth, error) {
	ready, err := s.Redis.LLen(ctx, queuekeys.Ready).Result()
	if err != nil {
		return Depth{}, err
	}
	retry, err := s.Redis.ZCard(ctx, queuekeys.Retry).Result()
	if err != nil {
		return Depth{}, err
	}
	return Depth{Ready: ready, Retry: retry}, nil
}
