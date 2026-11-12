package drivesync

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobs"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testredis"
)

type lpush struct{ rdb redis.UniversalClient }

func (l lpush) LPush(ctx context.Context, key, value string) error {
	return l.rdb.LPush(ctx, key, value).Err()
}

// The schedule as the worker runs it: the tick seeded twice, as two
// workers starting would, parked in the consumer's scheduled set until its
// moment, then run; the connection that was due gets its sync as a message
// of its own, which the same consumer takes and runs against the stub
// Drive, so a file that changed in Drive is fetched with nobody asking.
// And the ticks keep coming, with nothing but the messages keeping time.
func TestADueConnectionIsSyncedByTheTick(t *testing.T) {
	// A Redis database of this package's own, emptied (internal/testredis).
	rdb := testredis.Open(t, testredis.DriveSync)
	s := newSite(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// An imported file, indexed, with the connection's place in the feed
	// kept; then the file changes in Drive and the connection falls due.
	s.google.put(pdf(reportID, "Report.pdf", "version one", "2026-11-01T10:00:00.000Z"))
	report := s.picked(s.owner, s.connector, reportID, "Report.pdf", "application/pdf")
	if err := s.run(reportID); err != nil {
		t.Fatal(err)
	}
	s.indexedAsReady(report)
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	s.google.put(pdf(reportID, "Report.pdf", "version two", "2026-11-03T10:00:00.000Z"))

	routes := routing.Table{Default: "probe"}
	tick := &Tick{
		Schedule:  DueStore{Pool: s.pool},
		Publisher: produce.Producer{Broker: lpush{rdb}, Routes: routes, Origin: produce.Origin(), ReplyTo: "probe"},
		Fence:     RedisFence{Redis: rdb},
		// A second apart, so the test sees several.
		Every: time.Second, Batch: 10, Log: s.job.Log, Now: time.Now,
	}
	if err := tick.Check(); err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		ticks int
		syncs int
	)
	table, err := jobs.New(s.job.Log,
		jobs.Job{Task: TickTask, Deadline: time.Minute, Run: func(ctx context.Context, msg *celery.Message) error {
			mu.Lock()
			ticks++
			mu.Unlock()
			return tick.Run(ctx, msg)
		}},
		jobs.Job{Task: Task, Deadline: time.Minute, Run: func(ctx context.Context, msg *celery.Message) error {
			mu.Lock()
			syncs++
			mu.Unlock()
			return s.job.Run(ctx, msg)
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
		if err := tick.Seed(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if waiting, err := rdb.LLen(ctx, "probe").Result(); err != nil || waiting != 1 {
		t.Fatalf("two seeds left %d ticks on the queue (%v), want one", waiting, err)
	}

	running, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- consumer.Run(running) }()
	defer func() {
		stop()
		<-done
	}()

	deadline := time.After(30 * time.Second)
	for {
		mu.Lock()
		seenTicks, seenSyncs := ticks, syncs
		mu.Unlock()
		got, _ := s.document(report)
		if seenTicks >= 3 && got.Files == 2 {
			if s.stored(got.NewestKey) != "version two" {
				t.Fatalf("the tick's sync stored %q", s.stored(got.NewestKey))
			}
			// Due once: the connection was moved on a day, so the later
			// ticks published nothing more for it.
			if seenSyncs != 1 {
				t.Fatalf("%d syncs ran for one due connection over %d ticks", seenSyncs, seenTicks)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("after thirty seconds: %d ticks, %d syncs, the document %+v", seenTicks, seenSyncs, got)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
