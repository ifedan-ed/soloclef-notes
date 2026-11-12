package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_schema/schemaversion"
)

// EnvDatabaseURL names the Postgres the jobs read and write. It has no
// default, and a worker without it does not start.
const EnvDatabaseURL = "SOLOCLEF_WORKER_DATABASE_URL"

// databaseTimeout bounds the connection and the schema check made at
// start-up. A database that does not answer in this long is down.
const databaseTimeout = 30 * time.Second

// openDatabase opens the pool the jobs share and, before any job is taken,
// checks that the database is at the schema version this build was made
// against: a database at another version is a queue of jobs failing one by
// one. It never migrates; `migrate up` is a deployment's own step
// (soloclef_schema/schemaversion says why).
func openDatabase(ctx context.Context, getenv func(string) string) (*pgxpool.Pool, error) {
	address := strings.TrimSpace(getenv(EnvDatabaseURL))
	if address == "" {
		return nil, fmt.Errorf("%s is unset, and this worker guesses no database", EnvDatabaseURL)
	}
	ctx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		// pgx's own error can repeat the address, password included, so
		// only its kind is passed on.
		return nil, fmt.Errorf("%s: %s", EnvDatabaseURL, kindOf(err))
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: the database is unreachable: %s", EnvDatabaseURL, kindOf(err))
	}
	if err := schemaversion.Check(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// kindOf describes a connection failure without the address it was for.
func kindOf(err error) string {
	switch {
	case strings.Contains(err.Error(), "cannot parse"):
		return "it cannot be parsed as a Postgres address"
	case strings.Contains(err.Error(), "password authentication failed"):
		return "the password was refused"
	case strings.Contains(err.Error(), "connection refused"):
		return "the connection was refused"
	case strings.Contains(err.Error(), "context deadline exceeded"):
		return "it did not answer in time"
	default:
		return "the connection failed"
	}
}
