package jobreconcile

import (
	"context"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

// The outbox statement against Postgres: only a job that is queued and has
// been left alone long enough is taken, each is taken once, and nothing
// else about any job changes.
func TestOnlyJobsQueuedTooLongAreTaken(t *testing.T) {
	pool := testdatabase.Open(t)
	ctx := context.Background()
	owner := testdatabase.Account(t, pool)
	other := testdatabase.Account(t, pool)
	// Another test's leftovers in this shared database must not be this
	// test's stale jobs: nothing else may be queued and old.
	var strays int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM deliverable_jobs WHERE status = 'queued' AND updated_at < now() - interval '1 minute'`).Scan(&strays); err != nil {
		t.Fatal(err)
	}
	if strays != 0 {
		t.Fatalf("the test database already holds %d old queued jobs", strays)
	}
	testdatabase.Exec(t, pool, `
		INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, celery_task_id, attempt_count, updated_at) VALUES
		 (9300001, $1::uuid, 'video', 'lost, older',  'a', 'queued',  'deliverable-job:9300001:attempt:2', 2, now() - interval '10 minutes'),
		 (9300002, $2::uuid, 'video', 'lost, newer',  'b', 'queued',  'deliverable-job:9300002:attempt:1', 1, now() - interval '5 minutes'),
		 (9300003, $1::uuid, 'video', 'just queued',  'c', 'queued',  'deliverable-job:9300003:attempt:1', 1, now() - interval '30 seconds'),
		 (9300004, $1::uuid, 'video', 'running',      'd', 'running', 'deliverable-job:9300004:attempt:1', 1, now() - interval '10 minutes'),
		 (9300005, $1::uuid, 'video', 'failed',       'e', 'failed',  'deliverable-job:9300005:attempt:1', 1, now() - interval '10 minutes'),
		 (9300006, $1::uuid, 'video', 'no task id',   'f', 'queued',  NULL,                                1, now() - interval '3 minutes')`,
		owner, other)
	store := Store{Pool: pool}

	// A batch smaller than the work takes the oldest first.
	first, err := store.TakeStale(ctx, 2*time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || !has(first, Queued{9300001, "deliverable-job:9300001:attempt:2"}) || !has(first, Queued{9300002, "deliverable-job:9300002:attempt:1"}) {
		t.Fatalf("the first batch is %+v; want the two oldest, of either account", first)
	}
	// The next call takes what was left, and not the two just stamped.
	second, err := store.TakeStale(ctx, 2*time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0] != (Queued{ID: 9300006}) {
		t.Fatalf("the second batch is %+v; want only the job that names no task id", second)
	}
	if third, err := store.TakeStale(ctx, 2*time.Minute, 100); err != nil || len(third) != 0 {
		t.Fatalf("a third call took %+v, %v; every stale job was stamped", third, err)
	}

	// Stamped, and otherwise as they were.
	var unchanged int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM deliverable_jobs
		WHERE (id IN (9300001, 9300002, 9300006) AND status = 'queued' AND updated_at > now() - interval '1 minute')
		   OR (id = 9300001 AND attempt_count <> 2)
		   OR (id = 9300003 AND status = 'queued' AND updated_at < now() - interval '20 seconds')
		   OR (id = 9300004 AND status = 'running' AND updated_at < now() - interval '9 minutes')
		   OR (id = 9300005 AND status = 'failed' AND updated_at < now() - interval '9 minutes')`).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged != 6 {
		t.Errorf("%d of the 6 rows are as they should be", unchanged)
	}
}

func has(jobs []Queued, wanted Queued) bool {
	for _, job := range jobs {
		if job == wanted {
			return true
		}
	}
	return false
}
