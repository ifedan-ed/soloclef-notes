package drivesync

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// DueStore is Schedule over the pool.
type DueStore struct {
	Pool *pgxpool.Pool
}

// TakeDue is one statement: choose, move on, return.
//
// It names no owner, because it is not about an account: it sweeps the
// schedule of every account, and what it returns is only ever published,
// as a job that names the owner it read off the row and whose every
// statement names that owner again.
//
// The inner read is the partial index ix_search_source_connectors_due
// (enabled, periodic connections by next_scheduled_at), so a tick with
// nothing due is one index probe. SKIP LOCKED lets two ticks that overlap
// share the work. The choice is a materialised CTE for the reason
// jobreconcile's is: as a subquery its LIMIT bounds nothing.
//
// A connection Google has refused (`auth_expired`) is not due: nothing can
// be read with it until the person connects again, which clears the flag.
// The next time is counted from now, not from the time that was missed, so
// a connection that was due all through a long stop is synced once.
func (s DueStore) TakeDue(ctx context.Context, limit int) ([]Due, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH due AS MATERIALIZED (
			SELECT id FROM search_source_connectors
			WHERE periodic_indexing_enabled AND enabled AND next_scheduled_at <= now()
			  AND connector_type = 'GOOGLE_DRIVE_CONNECTOR'
			  AND indexing_frequency_minutes > 0
			  AND COALESCE(config ->> 'auth_expired', 'false') <> 'true'
			ORDER BY next_scheduled_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE search_source_connectors AS connection
		SET next_scheduled_at = now() + make_interval(mins => connection.indexing_frequency_minutes)
		FROM due
		WHERE connection.id = due.id
		RETURNING connection.id, connection.user_id::text`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Due, error) {
		var due Due
		err := row.Scan(&due.ConnectorID, &due.UserID)
		return due, err
	})
}

// fencePrefix is the tick's fence keys: one a moment, each expiring by
// itself. It is this tick's own prefix: sharing the reconciler's would let
// whichever of the two asked first for a moment keep the other from it.
const fencePrefix = "soloclef:worker:drive-tick:"

// RedisFence is Fence in the broker's own Redis: where the ticks are, so a
// Redis that was emptied forgets the fences along with the ticks they
// guarded.
type RedisFence struct {
	Redis redis.UniversalClient
}

func fenceKey(moment time.Time) string {
	return fencePrefix + strconv.FormatInt(moment.Unix(), 10)
}

func (f RedisFence) Take(ctx context.Context, moment time.Time, hold time.Duration) (bool, error) {
	return f.Redis.SetNX(ctx, fenceKey(moment), "1", hold).Result()
}

func (f RedisFence) Release(ctx context.Context, moment time.Time) error {
	return f.Redis.Del(ctx, fenceKey(moment)).Err()
}
