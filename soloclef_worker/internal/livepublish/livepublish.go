// Package livepublish is the worker's and the indexer's end of the
// live-change bus (soloclef_notify/livechange): what they publish when they
// write a row a person's open web tab or app shows, so it updates without a
// reload or a poll.
//
// It publishes on the process's one Redis connection (process.OpenRedis),
// the broker. Pub/sub channels belong to the Valkey server and not to a
// database number, and in every deployment the broker and the API's
// application Valkey are one server (docker-compose.go.yml's valkey-url):
// the API's subscriber hears these as it hears its own. The presence read
// before a push (pushdelivery) rests on the same fact.
package livepublish

import (
	"context"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
)

// New is the publisher over the process's Redis client.
func New(rdb redis.UniversalClient, logger *slog.Logger) livechange.Bus {
	return livechange.Bus{
		Send: func(ctx context.Context, channel string, payload []byte) error {
			return rdb.Publish(ctx, channel, payload).Err()
		},
		Logger: logger,
	}
}
