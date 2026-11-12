package jobreconcile

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

// TakeStale is one statement: choose, stamp, return.
//
// It is the one statement in this worker that names no owner, because it is
// not about an account: it sweeps the outbox of every account, and what it
// returns is only ever published, to a worker whose claim reads the owner
// off the row.
//
// The inner read is the partial index ix_deliverable_jobs_outbox (queued
// rows by updated_at), so a tick with nothing to do is one index probe.
// SKIP LOCKED lets two ticks that overlap share the work instead of one
// waiting for the other, and lets a job a worker is claiming this instant
// be left alone. The choice is a materialised CTE and not a subquery in
// FROM: as a subquery the planner may run it once for every row it joins
// to, and each run, skipping the rows the last one locked, chooses more, so
// the LIMIT bounds nothing (the database test caught exactly that).
func (s Store) TakeStale(ctx context.Context, staleAfter time.Duration, limit int) ([]Queued, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH stale AS MATERIALIZED (
			SELECT id FROM deliverable_jobs
			WHERE status = 'queued' AND updated_at <= now() - make_interval(secs => $1)
			ORDER BY updated_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE deliverable_jobs AS job
		SET updated_at = now()
		FROM stale
		WHERE job.id = stale.id AND job.status = 'queued'
		RETURNING job.id, COALESCE(job.celery_task_id, '')`, staleAfter.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Queued, error) {
		var job Queued
		err := row.Scan(&job.ID, &job.TaskID)
		return job, err
	})
}
