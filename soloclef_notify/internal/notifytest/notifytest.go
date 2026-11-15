// Package notifytest gives this module's database tests a throwaway
// Postgres brought to the current schema by soloclef_schema's migrate
// command, an account to own their rows, and a publisher that records what
// would have been queued.
//
// The database is named by SOLOCLEF_NOTIFY_TEST_DATABASE_URL and never
// defaulted: without it every test that asks is skipped, so a plain
// `go test ./...` touches no database. Each test makes an account of its own
// and deletes it afterwards; every row written belongs to an account and
// goes with it.
package notifytest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// Variable names the database.
const Variable = "SOLOCLEF_NOTIFY_TEST_DATABASE_URL"

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

// Account makes an account and removes it, with everything it owns, when
// the test ends.
func Account(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO "user" (email, is_verified) VALUES ($1, true) RETURNING id::text`,
		"notify-"+hex.EncodeToString(raw)+"@example.test").Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("removing the test account: %v", err)
		}
	})
	return id
}

// Int64 runs a statement that answers one bigint.
func Int64(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&value); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return value
}

// Exec runs a statement, failing the test on an error.
func Exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// Publisher records every message instead of queuing it.
type Publisher struct {
	mu       sync.Mutex
	messages []produce.Message
	// Fail, when set, is returned by every Send.
	Fail error
}

// Send is notify.Publisher.
func (p *Publisher) Send(_ context.Context, message produce.Message) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Fail != nil {
		return "", p.Fail
	}
	p.messages = append(p.messages, message)
	return "id", nil
}

// Messages is everything sent so far.
func (p *Publisher) Messages() []produce.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]produce.Message(nil), p.messages...)
}
