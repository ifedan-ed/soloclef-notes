package pushdelivery

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_notify/presence"
)

// Presence is delivery.Presence over the worker's Redis client: one ZCOUNT
// per account, all in one round trip.
type Presence struct {
	Redis redis.UniversalClient
}

// Active is delivery.Presence.
func (p Presence) Active(ctx context.Context, userIDs []string, now time.Time) (map[string]bool, error) {
	if len(userIDs) == 0 {
		return map[string]bool{}, nil
	}
	pipe := p.Redis.Pipeline()
	counts := make([]*redis.IntCmd, len(userIDs))
	for i, userID := range userIDs {
		counts[i] = pipe.ZCount(ctx, presence.Key(userID), presence.LiveFrom(now), "+inf")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	looking := make(map[string]bool, len(userIDs))
	for i, userID := range userIDs {
		looking[userID] = counts[i].Val() > 0
	}
	return looking, nil
}

// Fence is delivery.Fence: SET NX with an expiry.
type Fence struct {
	Redis redis.UniversalClient
}

// Take is delivery.Fence.
func (f Fence) Take(ctx context.Context, key string, hold time.Duration) (bool, error) {
	return f.Redis.SetNX(ctx, key, "1", hold).Result()
}

// Broker is produce.Broker over the worker's Redis client.
type Broker struct {
	Redis redis.UniversalClient
}

// LPush is produce.Broker.
func (b Broker) LPush(ctx context.Context, key, value string) error {
	return b.Redis.LPush(ctx, key, value).Err()
}
