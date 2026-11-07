// Package database opens this service's connection pool.
//
// The pool is capped by configuration because Postgres' connection limit is
// shared with the Python tier and its workers: replicas of this service are the
// point of moving a route here, and ten of them with an uncapped pool would
// take the database down (docker/postgresql.conf, and the API pool cap in
// app/db.py).
package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open returns a pool that has answered one query, so a bad URL or an
// unreachable database fails at startup rather than on the first request.
func Open(ctx context.Context, url string, maxConnections int32) (*pgxpool.Pool, error) {
	settings, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("database url: %w", err)
	}
	settings.MaxConns = maxConnections

	pool, err := pgxpool.NewWithConfig(ctx, settings)
	if err != nil {
		return nil, fmt.Errorf("database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database unreachable: %w", err)
	}
	return pool, nil
}
