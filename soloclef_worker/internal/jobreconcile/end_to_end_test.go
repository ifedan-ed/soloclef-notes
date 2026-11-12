package jobreconcile

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobs"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testredis"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/videojob"
)

// The reconciler as the worker runs it: seeded twice, as two workers
// starting would, taken by the real consumer through its scheduled set,
// against a real outbox. A job that was queued and never published reaches
// the video job's handler under its own task id, once; and the ticks keep
// coming, one an interval, with nothing but the messages themselves
// keeping time.
func TestALostJobIsPublishedAndTheTicksKeepComing(t *testing.T) {
	// A Redis database of this package's own, emptied (internal/testredis).
	rdb := testredis.Open(t, testredis.JobReconcile)
	pool := testdatabase.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	owner := testdatabase.Account(t, pool)
	var jobID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO deliverable_jobs (user_id, kind, title, tool_call_id, status, celery_task_id, attempt_count, updated_at)
		VALUES ($1::uuid, 'video', 'lost', 'call', 'queued', 'pending', 2, now() - interval '10 minutes') RETURNING id`, owner).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	taskID := "deliverable-job:" + strconv.FormatInt(jobID, 10) + ":attempt:2"
	testdatabase.Exec(t, pool, `UPDATE deliverable_jobs SET celery_task_id = $2, updated_at = now() - interval '10 minutes' WHERE id = $1 AND user_id = $3::uuid`, jobID, taskID, owner)

	routes := routing.Table{Default: "probe"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reconcile := &Job{
		Rows:      Store{Pool: pool},
		Publisher: produce.Producer{Broker: RedisBroker{Redis: rdb}, Routes: routes, Origin: produce.Origin(), ReplyTo: "probe"},
		Fence:     RedisFence{Redis: rdb},
		// A second apart, so the test sees several; a job is still only
		// republished after two minutes.
		Timing: Timing{Every: time.Second, StaleAfter: 2 * time.Minute, Batch: 100},
		Log:    log, Now: time.Now,
	}
	if err := reconcile.Check(); err != nil {
		t.Fatal(err)
	}

	var (
		mu     sync.Mutex
		ticks  []time.Time
		videos []*celery.Message
	)
	table, err := jobs.New(log,
		jobs.Job{Task: Task, Deadline: time.Minute, Run: func(ctx context.Context, msg *celery.Message) error {
			mu.Lock()
			ticks = append(ticks, time.Now())
			mu.Unlock()
			return reconcile.Run(ctx, msg)
		}},
		// In place of the render: what the video job would have been handed.
		jobs.Job{Task: videojob.Task, Deadline: time.Minute, Run: func(_ context.Context, msg *celery.Message) error {
			mu.Lock()
			videos = append(videos, msg)
			mu.Unlock()
			return nil
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	cfg := broker.DefaultConfig()
	cfg.Name = "probe-worker"
	cfg.Queues = routes.Queues()
	cfg.QueueConcurrency = map[string]int{"probe": 2, "probe.heavy": 2}
	consumer, err := broker.NewConsumer(rdb, cfg, table)
	if err != nil {
		t.Fatal(err)
	}

	// Two workers starting in the same second publish one tick between
	// them. (Seeded within one whole second, or the two are two moments.)
	for time.Now().Nanosecond() > int(700*time.Millisecond) {
		time.Sleep(10 * time.Millisecond)
	}
	for range 2 {
		if err := reconcile.Seed(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if waiting, err := rdb.LLen(ctx, "probe").Result(); err != nil || waiting != 1 {
		t.Fatalf("two seeds left %d ticks on the queue (%v), want one", waiting, err)
	}

	running, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- consumer.Run(running) }()
	defer func() {
		stop()
		<-done
	}()

	deadline := time.After(20 * time.Second)
	for {
		mu.Lock()
		seen, delivered := len(ticks), len(videos)
		mu.Unlock()
		if seen >= 4 && delivered >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("after 20s: %d ticks ran and %d jobs were published", seen, delivered)
		case <-time.After(50 * time.Millisecond):
		}
	}
	elapsed := time.Since(started)

	mu.Lock()
	defer mu.Unlock()
	// The lost job, once, as the message its claim accepts.
	if len(videos) != 1 {
		t.Fatalf("the job was published %d times over %d ticks; want once, the row being stamped", len(videos), len(ticks))
	}
	if got := videos[0]; got.ID != taskID || got.Kwargs["job_id"] != float64(jobID) || len(got.Args) != 0 || got.Queue != "probe.heavy" {
		t.Errorf("the video job was handed id %q, %v and %v on %q; want %q and job_id %d on the heavy queue",
			got.ID, got.Args, got.Kwargs, got.Queue, taskID, jobID)
	}
	// One chain: about a tick a second, never the two a second that two
	// seeds each starting a chain of their own would make.
	if most := int(elapsed/time.Second) + 2; len(ticks) > most {
		t.Errorf("%d ticks ran in %s; one chain makes at most %d", len(ticks), elapsed.Round(time.Millisecond), most)
	}
}
