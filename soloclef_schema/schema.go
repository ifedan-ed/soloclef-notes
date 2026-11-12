// Package schema applies Soloclef's migrations to a database.
//
// The migrations are the SQL files in migrations/, compiled in. goose runs
// them: each file in its own transaction, in number order, recording what
// has run in the goose_db_version table that schemaversion reads.
//
// Only the migrate command calls this package. A service never does; it
// calls schemaversion.Check and refuses a database that is not already at
// its version (the reason is in that package).
package schema

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"git.danvics.com/danvics/soloclef/soloclef_schema/migrations"
)

// Applied is one migration and when it reached the database; AppliedAt is
// zero for a migration that has not.
type Applied struct {
	Version   int64
	Name      string
	AppliedAt time.Time
}

// Pending reports whether the migration has yet to be applied.
func (a Applied) Pending() bool { return a.AppliedAt.IsZero() }

// Migrate applies every migration the database does not have yet and
// returns the ones it applied, in order. A database already at the newest
// version is left alone and nothing is returned.
//
// A migration that fails is rolled back with its transaction, the ones
// before it stay, and the error names the file.
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]Applied, error) {
	provider, done, err := open(pool)
	if err != nil {
		return nil, err
	}
	defer done()
	results, err := provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("schema: applying the migrations: %w", err)
	}
	return applied(results), nil
}

// Status lists every migration compiled into this binary, in order, with
// when it was applied to this database. It applies nothing. On a database
// that has never been migrated every migration is pending, and the one
// thing left behind is goose's own bookkeeping table, empty, which the
// runner creates before it can read it.
func Status(ctx context.Context, pool *pgxpool.Pool) ([]Applied, error) {
	provider, done, err := open(pool)
	if err != nil {
		return nil, err
	}
	defer done()
	statuses, err := provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("schema: reading the migration status: %w", err)
	}
	listed := make([]Applied, len(statuses))
	for index, status := range statuses {
		listed[index] = Applied{Version: status.Source.Version, Name: status.Source.Path, AppliedAt: status.AppliedAt}
	}
	return listed, nil
}

// DownTo rolls the database back until `version` is the newest migration
// applied, running each later migration's Down section, newest first.
// Version 0 removes everything the migrations created.
//
// It destroys data: the baseline's Down drops every table. It exists so
// that a migration's Down section can be exercised while the migration is
// being written, and the migrate command refuses to call it for a database
// it has not been told is a development one.
func DownTo(ctx context.Context, pool *pgxpool.Pool, version int64) ([]Applied, error) {
	if version < 0 {
		return nil, fmt.Errorf("schema: %d is not a version", version)
	}
	provider, done, err := open(pool)
	if err != nil {
		return nil, err
	}
	defer done()
	results, err := provider.DownTo(ctx, version)
	if err != nil {
		return nil, fmt.Errorf("schema: rolling back to version %d: %w", version, err)
	}
	return applied(results), nil
}

// open builds goose's runner over the pool. The *sql.DB it needs borrows
// the pool's connections and owns none, so closing it (done) leaves the
// pool as the caller gave it.
func open(pool *pgxpool.Pool) (*goose.Provider, func(), error) {
	if pool == nil {
		return nil, nil, errors.New("schema: no database pool was given")
	}
	// A session lock, held in Postgres for the length of a run: two migrate
	// commands started together (two operators, or a Compose `up` racing a
	// manual run) apply the files once, one after the other, and not
	// interleaved.
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, nil, fmt.Errorf("schema: %w", err)
	}
	database := stdlib.OpenDBFromPool(pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, database, migrations.Files,
		goose.WithSessionLocker(locker))
	if err != nil {
		_ = database.Close()
		return nil, nil, fmt.Errorf("schema: reading the migrations: %w", err)
	}
	return provider, func() { _ = database.Close() }, nil
}

func applied(results []*goose.MigrationResult) []Applied {
	listed := make([]Applied, len(results))
	now := time.Now()
	for index, result := range results {
		listed[index] = Applied{Version: result.Source.Version, Name: result.Source.Path, AppliedAt: now}
	}
	return listed
}
