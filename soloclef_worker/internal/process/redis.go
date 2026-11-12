package process

import (
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// EnvRedisURL is the broker and the result backend, which are one Redis in
// this app (app/celery_app.py). It has no default on purpose: a worker that
// guessed an address could take work off the live queues from a dev build.
const EnvRedisURL = "SOLOCLEF_WORKER_REDIS_URL"

// OpenRedis is the one connection this worker makes: the queues it consumes
// and the rows it writes are both here.
func OpenRedis(getenv func(string) string) (*redis.Client, error) {
	url := strings.TrimSpace(getenv(EnvRedisURL))
	if url == "" {
		return nil, fmt.Errorf("%s is unset, and this worker guesses no broker", EnvRedisURL)
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvRedisURL, err)
	}
	return redis.NewClient(options), nil
}
