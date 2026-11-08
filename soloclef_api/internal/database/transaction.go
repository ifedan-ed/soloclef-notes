package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Write runs one route's statements inside one transaction and commits it.
//
// Every query this tier runs is generated as :one or :many against a pool, and
// a read needs nothing more: one statement is its own transaction. A write does
// not. The Python routes hold a single SQLAlchemy session open across every
// statement they run and call commit() once at the end, so a route that reads a
// row, changes a second and inserts a third either does all three or none —
// and the refusals it raises in between leave nothing behind, because the
// session is rolled back.
//
// Reproducing that is the whole seam: work is handed a pgx.Tx, which satisfies
// each slice's generated DBTX, so the same generated queries run inside the
// transaction with no second way of reaching the database. The transaction is
// rolled back unless work returns without error and the commit succeeds, which
// also covers a panic and a cancelled request.
func Write[T any](
	ctx context.Context,
	pool *pgxpool.Pool,
	work func(context.Context, pgx.Tx, *Effects) (T, error),
) (T, error) {
	var nothing T

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nothing, fmt.Errorf("begin: %w", err)
	}
	// Harmless after a commit, and the only thing that runs on every other
	// path out of this function.
	defer tx.Rollback(ctx)

	effects := &Effects{}
	result, err := work(ctx, tx, effects)
	if err != nil {
		return nothing, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nothing, fmt.Errorf("commit: %w", err)
	}

	effects.run()
	return result, nil
}
