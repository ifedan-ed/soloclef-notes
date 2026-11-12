// Package testdatabase gives a job's database test a throwaway Postgres
// that soloclef_schema's migrate command has brought to the current
// schema, and an account to own its rows.
//
// The database is named by SOLOCLEF_WORKER_TEST_DATABASE_URL and never
// defaulted: without it every test that asks is skipped, so a plain
// `go test ./...` touches no database. Each test makes an account of its
// own and deletes it afterwards; every row a job writes belongs to an
// account and goes with it.
package testdatabase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Variable names the database.
const Variable = "SOLOCLEF_WORKER_TEST_DATABASE_URL"

// Open connects, or skips the test.
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	address := os.Getenv(Variable)
	if address == "" {
		t.Skipf("%s is not set: start a throwaway Postgres 17, run soloclef_schema's `migrate up` against it, and name it here", Variable)
	}
	pool, err := pgxpool.New(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Account makes an account and removes it, and everything it owns, when
// the test ends.
func Account(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO "user" (email, is_verified) VALUES ($1, true) RETURNING id::text`,
		"job-"+hex.EncodeToString(raw)+"@example.test").Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("removing the test account: %v", err)
		}
	})
	return id
}

// Exec runs statements, failing the test on an error.
func Exec(t *testing.T, pool *pgxpool.Pool, sql string, arguments ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, arguments...); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
}
