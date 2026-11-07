package broker

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Depths is how much work is waiting on each queue: the sum of its priority
// lists, because a queue is several Redis lists and reading only the bare name
// would under-report.
//
// It is the one number `celery_heavy` never had: a fifth podcast request used
// to wait with nothing reporting that it was waiting.
func Depths(ctx context.Context, rdb redis.UniversalClient, queues []string) (map[string]int64, error) {
	pipe := rdb.Pipeline()
	lengths := make(map[string][]*redis.IntCmd, len(queues))
	for _, queue := range queues {
		for _, key := range QueueKeys(queue) {
			lengths[queue] = append(lengths[queue], pipe.LLen(ctx, key))
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("broker: reading queue depth: %w", err)
	}

	depths := make(map[string]int64, len(queues))
	for queue, commands := range lengths {
		for _, command := range commands {
			depths[queue] += command.Val()
		}
	}
	return depths, nil
}
