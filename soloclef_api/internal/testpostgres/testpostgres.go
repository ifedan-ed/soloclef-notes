// Package testpostgres gives an opt-in database test a real Postgres holding
// the schema the migrations create, and two accounts to put rows under.
//
// It exists because a route test that uses a stand-in for the database
// cannot prove the one thing every route now depends on: that each statement
// names the owner, so one account never reads or changes another's row. That
// is a property of the SQL and only Postgres can be asked about it. Every
// package's isolation test therefore begins the same way -- an empty schema,
// account A, account B -- and this is that beginning, written once.
//
// Test support only: nothing outside a _test.go file imports it.
package testpostgres

import (
	"context"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_schema/migrations"
)

// The two accounts every isolation test uses. Alice owns the rows a test
// makes; Bob is the other account, who must be able to see and change none
// of them.
const (
	Alice = "11111111-1111-4111-8111-111111111111"
	Bob   = "22222222-2222-4222-8222-222222222222"
)

// Open connects to the database the environment variable names, gives it
// the schema if it has none, empties it and adds the two accounts.
//
// With the variable unset the test is skipped: these tests need a Postgres
// they are free to empty. The variable must therefore never name a database
// anyone uses -- **every account in it, and everything every account owns,
// is deleted**. Each package has a variable of its own so that packages
// tested at the same time are given different databases and do not empty
// each other's.
func Open(t testing.TB, variable string) *pgxpool.Pool {
	t.Helper()
	address := os.Getenv(variable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", variable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	t.Cleanup(pool.Close)
	migrate(t, pool)
	// Every owned table references "user" with ON DELETE CASCADE, so
	// truncating it with CASCADE empties them all without this package
	// having to know their names. RESTART IDENTITY so that a test's
	// explicit ids never meet a sequence left high by an earlier run.
	if _, err := pool.Exec(ctx, `TRUNCATE "user" RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("emptying the database: %v", err)
	}
	Exec(t, pool, `
INSERT INTO "user" (id, email) VALUES
 ('`+Alice+`', 'alice@example.test'),
 ('`+Bob+`', 'bob@example.test')`)
	return pool
}

// Exec runs a test's own seed. With no arguments pgx sends the text as one
// simple query, which is the only protocol that takes several statements at
// once, so a seed can be written as the SQL it is.
func Exec(t testing.TB, pool *pgxpool.Pool, statements string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), statements); err != nil {
		t.Fatalf("seeding the database: %v", err)
	}
}

// migrate gives an empty database the schema, from the same files the
// migrate command applies (soloclef_schema/migrations, compiled in), so a
// test can never run against a schema the migrations do not produce.
//
// It runs each file's Up section itself rather than calling the migration
// runner: that would make this module depend on the runner for the sake of
// its tests alone. The cost is that it does not keep the runner's record of
// what was applied, so it only ever fills a database that has no schema at
// all, and a database left over from an older set of migrations must be
// dropped, not upgraded. That is what "throwaway" means here.
func migrate(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var present bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public."user"') IS NOT NULL`).Scan(&present); err != nil {
		t.Fatalf("asking whether the database has a schema: %v", err)
	}
	if present {
		return
	}
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil || len(names) == 0 {
		t.Fatalf("the compiled-in migrations: %v, %v", names, err)
	}
	// The numeric prefix is zero-padded, so name order is version order.
	sort.Strings(names)
	for _, name := range names {
		content, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		// A file is "-- +goose Up", statements, "-- +goose Down",
		// statements. Only the first half builds the schema.
		up, _, _ := strings.Cut(string(content), "-- +goose Down")
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("applying %s: %v", name, err)
		}
	}
}
