// Package results writes Celery's result rows where Python reads them. It is
// the other half of "only the consumer moves": a job this worker ran must be
// indistinguishable, to AsyncResult and to everything polling it, from one a
// Celery worker ran.
package results

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"github.com/redis/go-redis/v9"
)

// CeleryResultExpiry is result_expires in app/celery_app.py. Rows outlive the
// clients that poll them by a day and then go, as Celery's own do.
const CeleryResultExpiry = 24 * time.Hour

// Backend is the Redis result backend, writing only.
type Backend struct {
	rdb    redis.UniversalClient
	expiry time.Duration
}

// NewBackend refuses a backend that would write rows nothing clears.
func NewBackend(rdb redis.UniversalClient, expiry time.Duration) (*Backend, error) {
	if rdb == nil {
		return nil, errors.New("results: no redis client")
	}
	if expiry <= 0 {
		return nil, errors.New("results: expiry must be positive, as result_expires is")
	}
	return &Backend{rdb: rdb, expiry: expiry}, nil
}

// Write stores one row and announces it, exactly as Celery's Redis backend
// does in one pipeline: the stored row serves everything that polls, and the
// publish on the same key is what releases an AsyncResult.get() waiting on it.
func (b *Backend) Write(ctx context.Context, row celery.Result) error {
	payload, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("results: encoding the row for %s: %w", row.TaskID, err)
	}
	key := celery.ResultKey(row.TaskID)

	pipe := b.rdb.Pipeline()
	pipe.SetEx(ctx, key, payload, b.expiry)
	pipe.Publish(ctx, key, payload)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("results: writing %s for %s: %w", row.State, row.TaskID, err)
	}
	return nil
}
