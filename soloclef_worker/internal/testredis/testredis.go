// Package testredis gives each test package a Redis database of its own on
// the one throwaway server SOLOCLEF_WORKER_TEST_REDIS names.
//
// Why a database each. Every one of these tests starts by emptying its
// Redis, so that it sees only its own queues. `go test ./...` runs
// packages side by side, and while they all emptied the same database, one
// package's flush removed another's queued message mid-test: broker,
// drivesync and jobreconcile failed unless the run was forced serial with
// `-p 1`. A Redis server holds sixteen numbered databases that share
// nothing, and FLUSHDB empties only the one a connection has selected, so a
// number per package keeps the flush and takes away what it could reach.
//
// The numbers are listed here, in one place, so that two packages cannot
// be given the same one by accident. A new package that flushes takes the
// next number.
//
// The server is named by the environment and never defaulted: without it
// every test that asks is skipped, so a plain `go test ./...` touches no
// Redis.
package testredis

import (
	"context"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

// Variable names the throwaway server. A database number in it is ignored:
// each package uses its own.
const Variable = "SOLOCLEF_WORKER_TEST_REDIS"

// Database is one package's database number on that server. Zero is left
// unused on purpose: it is the database a client gets when it names none,
// so it is where a real queue would be if the address were ever pointed at
// a server that matters.
type Database int

const (
	Broker Database = iota + 1
	Beat
	JobReconcile
	DriveSync
	AutomationTriggers
	WorkerCommand
)

// liveQueues are the queues real deployments have used. One of them in
// database 0 means the address points somewhere it must not.
var liveQueues = []string{
	"surfsense", "surfsense.connectors", "surfsense.heavy",
	"soloclef", "soloclef.heavy", "soloclef.index",
}

// marker is the key Open leaves in a database it has emptied. A numbered
// database that holds keys and not this one was filled by something else,
// and is not emptied. (Queue names cannot tell the two apart there: the
// tests use some of the real names for their own queues.)
const marker = "soloclef-worker-test:emptied-by"

// Open connects to the package's database and empties it, or skips the test
// when no server is named. It refuses a server that holds a live queue in
// database 0, and a numbered database that something else has filled.
func Open(t *testing.T, database Database) *redis.Client {
	t.Helper()
	address := os.Getenv(Variable)
	if address == "" {
		t.Skipf("set %s to a throwaway Redis (the test empties database %d of it)", Variable, database)
	}
	options, err := redis.ParseURL(address)
	if err != nil {
		t.Fatalf("%s: %v", Variable, err)
	}
	ctx := context.Background()

	// Database 0 is looked at and never written: it is only where a live
	// queue would show.
	options.DB = 0
	guard := redis.NewClient(options)
	defer guard.Close()
	if err := guard.Ping(ctx).Err(); err != nil {
		t.Fatalf("%s is unreachable: %v", Variable, err)
	}
	for _, queue := range liveQueues {
		if n, err := guard.Exists(ctx, queue).Result(); err == nil && n > 0 {
			t.Fatalf("%s holds the live queue %q; refusing to touch that server", Variable, queue)
		}
	}

	mine := *options
	mine.DB = int(database)
	rdb := redis.NewClient(&mine)
	t.Cleanup(func() { _ = rdb.Close() })
	// The first command on the numbered database, so a server set up with
	// fewer databases than this package's number says so here.
	size, err := rdb.DBSize(ctx).Result()
	if err != nil {
		t.Fatalf("reading database %d of the test Redis: %v", database, err)
	}
	if size > 0 {
		if n, err := rdb.Exists(ctx, marker).Result(); err != nil || n == 0 {
			t.Fatalf("database %d of %s holds %d key(s) these tests did not leave; refusing to empty it", database, Variable, size)
		}
	}
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("emptying database %d of the test Redis: %v", database, err)
	}
	// No expiry: the tests that count keys count their own by name, and a
	// marker that lapsed mid-run would lock the next run out.
	if err := rdb.Set(ctx, marker, t.Name(), 0).Err(); err != nil {
		t.Fatalf("marking database %d of the test Redis: %v", database, err)
	}
	return rdb
}
