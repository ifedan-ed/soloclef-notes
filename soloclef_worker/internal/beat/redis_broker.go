package beat

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// redisBroker is this tier's Redis client behind produce.Broker. LPUSH is the
// end kombu's producer writes and the end BRPOP's consumers read last.
type redisBroker struct {
	rdb redis.UniversalClient
}

func (b redisBroker) LPush(ctx context.Context, key, value string) error {
	return b.rdb.LPush(ctx, key, value).Err()
}
