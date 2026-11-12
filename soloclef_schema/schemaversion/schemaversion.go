// Package schemaversion answers one question at start-up: is this database
// at the schema version this binary was built against?
//
// A service never migrates the database it starts on. Bringing a database
// to a version is somebody's decision, made by running the migrate command;
// a service that did it on boot would let a rolling restart, or a stale
// binary started by mistake, change the schema under every other process.
// So a service only looks, and refuses to start when the answer is no:
// behind, and its queries name columns that are not there; ahead, and it
// would write rows the newer schema no longer accepts. Either way the
// failure is one process that will not start and says why, instead of
// requests failing one at a time.
//
// The version a build expects is not written down anywhere: it is the
// migrations compiled into it. This package reads what goose recorded in
// the database and does not import goose, so a service that only checks
// does not carry the migration runner.
package schemaversion

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_schema/migrations"
)

// Table is where goose records the migrations it has applied: one row per
// applied version. Rolling a version back deletes its row.
const Table = "goose_db_version"

// MigrateCommand is what an operator runs to bring a database to this
// build's version; the refusals name it.
const MigrateCommand = "migrate up"

// Database is the part of a pgx pool or connection the check uses.
type Database interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Expected lists the versions of the migrations compiled into this binary,
// ascending. A file that does not start with a number is a build error
// here and not a migration silently left out.
func Expected() ([]int64, error) {
	return versionsIn(migrations.Files)
}

func versionsIn(files fs.FS) ([]int64, error) {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errors.New("schemaversion: this binary was built with no migrations in it")
	}
	versions := make([]int64, 0, len(names))
	for _, name := range names {
		number, _, found := strings.Cut(name, "_")
		version, err := strconv.ParseInt(number, 10, 64)
		if !found || err != nil || version < 1 {
			return nil, fmt.Errorf("schemaversion: migration %q is not named <number>_<name>.sql", name)
		}
		if slices.Contains(versions, version) {
			return nil, fmt.Errorf("schemaversion: two migrations are numbered %d", version)
		}
		versions = append(versions, version)
	}
	slices.Sort(versions)
	return versions, nil
}

// Latest is the newest version compiled into this binary: the version a
// database must be at for this binary to start.
func Latest() (int64, error) {
	versions, err := Expected()
	if err != nil {
		return 0, err
	}
	return versions[len(versions)-1], nil
}

// Check returns nil when the database has exactly the migrations this
// binary was built with, and otherwise an error that says which way it
// differs and what to do. It changes nothing.
func Check(ctx context.Context, database Database) error {
	expected, err := Expected()
	if err != nil {
		return err
	}
	applied, err := Applied(ctx, database)
	if err != nil {
		return err
	}
	return compare(expected, applied)
}

// Applied reads the versions goose has recorded as applied, ascending. A
// database goose has never touched is an error, not an empty answer: it is
// either empty or was built by something else, and neither is a database a
// service may start on.
func Applied(ctx context.Context, database Database) ([]int64, error) {
	// Looked up through the search path, as goose's own unqualified name is.
	var present bool
	if err := queryOne(ctx, database, &present, `SELECT to_regclass($1) IS NOT NULL`, Table); err != nil {
		return nil, fmt.Errorf("schemaversion: reading the schema: %w", err)
	}
	if !present {
		return nil, fmt.Errorf("schemaversion: this database has no %s table, so it has never been migrated; "+
			"run `%s` against it before starting a service", Table, MigrateCommand)
	}
	// Version 0 is goose's own first row, not a migration.
	rows, err := database.Query(ctx, `SELECT version_id FROM `+Table+` WHERE is_applied AND version_id > 0 ORDER BY version_id`)
	if err != nil {
		return nil, fmt.Errorf("schemaversion: reading %s: %w", Table, err)
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("schemaversion: reading %s: %w", Table, err)
	}
	// One row per version is what the runner writes; Compact keeps a
	// hand-edited table from being read as two different versions.
	return slices.Compact(applied), nil
}

func queryOne(ctx context.Context, database Database, into any, sql string, args ...any) error {
	rows, err := database.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return pgx.ErrNoRows
	}
	if err := rows.Scan(into); err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}

// compare is the decision, apart from the database so that every case of it
// is tested without one.
func compare(expected, applied []int64) error {
	var missing, unknown []int64
	for _, version := range expected {
		if !slices.Contains(applied, version) {
			missing = append(missing, version)
		}
	}
	for _, version := range applied {
		if !slices.Contains(expected, version) {
			unknown = append(unknown, version)
		}
	}
	latest := expected[len(expected)-1]
	switch {
	case len(missing) == 0 && len(unknown) == 0:
		return nil
	case len(unknown) > 0:
		// Said first when both are true: a database with a migration this
		// build has never heard of is not one `migrate up` from this build
		// can fix.
		return fmt.Errorf("schemaversion: the database has migrations this build does not know (%s); this build expects "+
			"version %d, so it is older than the database: deploy the build the database was migrated by",
			list(unknown), latest)
	default:
		return fmt.Errorf("schemaversion: the database is behind this build: it is at version %d and this build expects "+
			"version %d (not applied: %s); run `%s` against it, then start the service",
			newest(applied), latest, list(missing), MigrateCommand)
	}
}

func newest(versions []int64) int64 {
	if len(versions) == 0 {
		return 0
	}
	return slices.Max(versions)
}

func list(versions []int64) string {
	text := make([]string, len(versions))
	for index, version := range versions {
		text[index] = strconv.FormatInt(version, 10)
	}
	return strings.Join(text, ", ")
}
