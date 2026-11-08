package beat

import (
	"context"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"github.com/redis/go-redis/v9"
)

const probeQueue = "probe.beat"

// A beat bug is invisible until it has run for a day, so the clock is this
// worker's to move: the tests below put the scheduler at a chosen minute
// rather than waiting for one.
type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

func (c *testClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(by)
}

func probeEntry() Entry {
	return Entry{
		Name:    "probe-every-minute",
		Task:    "probe.beat.tick",
		Cron:    Crontab{Minute: "*"},
		Expires: 50 * time.Second,
		Queue:   probeQueue,
	}
}

func probeConfig(clock *testClock) Config {
	cfg := DefaultConfig()
	cfg.Entries = []Entry{probeEntry()}
	cfg.KeyPrefix = "probe-beat"
	cfg.Clock = clock.now
	cfg.PollInterval = time.Millisecond
	cfg.LeaseTTL = 2 * time.Second
	cfg.LeaseRenew = 500 * time.Millisecond
	cfg.LeaseRetry = 10 * time.Millisecond
	cfg.Origin = "gen1@probe"
	cfg.ReplyTo = "11111111-1111-4111-8111-111111111111"
	return cfg
}

func newBeat(t *testing.T, rdb redis.UniversalClient, cfg Config, name string) *Beat {
	t.Helper()
	cfg.Name = name
	b, err := New(rdb, cfg)
	if err != nil {
		t.Fatalf("New(%s): %v", name, err)
	}
	return b
}

func queued(t *testing.T, rdb redis.UniversalClient, queue string) []*celery.Message {
	t.Helper()
	raw, err := rdb.LRange(context.Background(), queue, 0, -1).Result()
	if err != nil {
		t.Fatalf("LRange %s: %v", queue, err)
	}
	messages := make([]*celery.Message, 0, len(raw))
	for _, item := range raw {
		msg, err := celery.Decode([]byte(item))
		if err != nil {
			t.Fatalf("a published tick is not readable: %v", err)
		}
		messages = append(messages, msg)
	}
	return messages
}

// The hazard this phase exists to rule out: two schedulers, one window, one
// message. The fence is per entry and per window, so it does not matter which
// of them gets there first or whether they overlap.
func TestATickFiresOnceWithTwoBeatsRacing(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	clock := &testClock{at: time.Date(2026, 11, 8, 13, 47, 0, 0, time.UTC)}

	first := newBeat(t, rdb, probeConfig(clock), "beat-a")
	second := newBeat(t, rdb, probeConfig(clock), "beat-b")

	// The first loop of a schedule never fires, as Celery's own does not: it
	// only records where the schedule now is.
	if err := first.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if err := second.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := queued(t, rdb, probeQueue); len(got) != 0 {
		t.Fatalf("the first loop published %d ticks, want none", len(got))
	}

	clock.advance(time.Minute)
	var wg sync.WaitGroup
	for _, b := range []*Beat{first, second, first, second} {
		wg.Add(1)
		go func(b *Beat) {
			defer wg.Done()
			if err := b.tick(ctx, clock.now()); err != nil {
				t.Errorf("tick: %v", err)
			}
		}(b)
	}
	wg.Wait()

	messages := queued(t, rdb, probeQueue)
	if len(messages) != 1 {
		t.Fatalf("four racing ticks published %d messages, want 1", len(messages))
	}
	if messages[0].Task != "probe.beat.tick" {
		t.Errorf("published %q", messages[0].Task)
	}
}

// A tick is a message a Python Celery worker has to be able to run, so the
// envelope is checked by the decoder the Python transport's output is tested
// against, and the delivery details a consumer needs are checked by name.
func TestATickIsAMessageCeleryCanConsume(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	clock := &testClock{at: time.Date(2026, 11, 8, 13, 47, 0, 0, time.UTC)}
	b := newBeat(t, rdb, probeConfig(clock), "beat-a")

	if err := b.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	clock.advance(time.Minute)
	if err := b.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	messages := queued(t, rdb, probeQueue)
	if len(messages) != 1 {
		t.Fatalf("published %d messages, want 1", len(messages))
	}
	msg := messages[0]
	if msg.Queue != probeQueue {
		t.Errorf("routing key = %q", msg.Queue)
	}
	if msg.ID == "" || msg.RootID != msg.ID {
		t.Errorf("id = %q, root_id = %q", msg.ID, msg.RootID)
	}
	if msg.DeliveryTag == "" {
		t.Error("no delivery tag: nothing could acknowledge this message")
	}
	if msg.EnqueuedAt.IsZero() {
		t.Error("no enqueue stamp: the queue wait of a scheduled task would be unknown")
	}
	// expires is computed at publish, as Celery computes it, so a tick that
	// waits out its window is dropped rather than run late.
	want := clock.now().Add(50 * time.Second)
	if !msg.Expires.Equal(want) {
		t.Errorf("expires = %v, want %v", msg.Expires, want)
	}
	if !msg.ETA.IsZero() {
		t.Error("a beat tick has no eta: it is due when it is published")
	}
}

// Celery Beat's scheduler collapses every window missed while it was down into
// one run, and that is what this does: a catch-up, not a backfill.
func TestAMissedWindowCatchesUpOnceRatherThanBackfilling(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	clock := &testClock{at: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)}
	b := newBeat(t, rdb, probeConfig(clock), "beat-a")

	if err := b.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	// Three hours down, which is 180 missed windows for an every-minute
	// entry.
	clock.advance(3 * time.Hour)
	restarted := newBeat(t, rdb, probeConfig(clock), "beat-restarted")
	if err := restarted.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if messages := queued(t, rdb, probeQueue); len(messages) != 1 {
		t.Fatalf("a three-hour gap published %d ticks, want 1", len(messages))
	}
	if caught := restarted.Snapshot().CaughtUp; caught != 1 {
		t.Errorf("CaughtUp = %d, want 1: a catch-up should be visible, not silent", caught)
	}
}

// Where the schedule is has to survive the process, or a restart is a missed
// window the next restart can never see.
func TestWhereTheScheduleIsSurvivesTheProcess(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	clock := &testClock{at: time.Date(2026, 11, 8, 13, 47, 0, 0, time.UTC)}

	first := newBeat(t, rdb, probeConfig(clock), "beat-a")
	if err := first.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// A new process, same database, inside the same minute: the window has
	// already been accounted for, so nothing fires.
	second := newBeat(t, rdb, probeConfig(clock), "beat-b")
	clock.advance(10 * time.Second)
	if err := second.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if messages := queued(t, rdb, probeQueue); len(messages) != 0 {
		t.Fatalf("a restart inside the same minute published %d ticks", len(messages))
	}
}

// A clock that went backwards must not re-run a window, and must not be
// mistaken for a missed one.
func TestAClockThatWentBackwardsFiresNothing(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	clock := &testClock{at: time.Date(2026, 11, 8, 13, 47, 0, 0, time.UTC)}
	b := newBeat(t, rdb, probeConfig(clock), "beat-a")

	if err := b.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	clock.advance(-2 * time.Hour)
	if err := b.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if messages := queued(t, rdb, probeQueue); len(messages) != 0 {
		t.Fatalf("a clock set back published %d ticks", len(messages))
	}
}

// Successive windows are separate fences, so fencing one must not stop the
// next.
func TestTheFenceStopsAWindowAndNotTheSchedule(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	clock := &testClock{at: time.Date(2026, 11, 8, 13, 47, 0, 0, time.UTC)}
	b := newBeat(t, rdb, probeConfig(clock), "beat-a")

	if err := b.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	for i := 0; i < 3; i++ {
		clock.advance(time.Minute)
		if err := b.tick(ctx, clock.now()); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if messages := queued(t, rdb, probeQueue); len(messages) != 3 {
		t.Fatalf("three minutes published %d ticks, want 3", len(messages))
	}
}

// Run is where the lease lives: a beat that does not hold it publishes
// nothing at all, however long it runs.
func TestOnlyTheLeaseHolderTicks(t *testing.T) {
	rdb := testRedis(t)
	clock := &testClock{at: time.Date(2026, 11, 8, 13, 47, 0, 0, time.UTC)}

	holder := newBeat(t, rdb, probeConfig(clock), "beat-holder")
	held, err := holder.lease.take(context.Background())
	if err != nil || !held {
		t.Fatalf("taking the lease: %v (held %v)", err, held)
	}
	defer func() { _ = holder.lease.release(context.Background()) }()

	waiting := newBeat(t, rdb, probeConfig(clock), "beat-waiting")
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- waiting.Run(ctx) }()

	clock.advance(5 * time.Minute)
	time.Sleep(200 * time.Millisecond)
	if messages := queued(t, rdb, probeQueue); len(messages) != 0 {
		t.Fatalf("a beat without the lease published %d ticks", len(messages))
	}
	if waiting.Snapshot().LeaseHeld {
		t.Error("Snapshot says the lease is held when it is not")
	}

	stop()
	if err := <-done; err != nil {
		t.Errorf("Run: %v", err)
	}
}

// The lease has to be given up when the process dies, which on Redis means it
// has to expire: a process killed with SIGKILL releases nothing.
func TestTheLeaseExpiresWhenTheHolderStops(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	clock := &testClock{at: time.Date(2026, 11, 8, 13, 47, 0, 0, time.UTC)}

	cfg := probeConfig(clock)
	cfg.LeaseTTL = 300 * time.Millisecond
	cfg.LeaseRenew = 100 * time.Millisecond
	dead := newBeat(t, rdb, cfg, "beat-dead")
	if held, err := dead.lease.take(ctx); err != nil || !held {
		t.Fatalf("taking the lease: %v (held %v)", err, held)
	}
	ttl, err := rdb.PTTL(ctx, dead.lease.key).Result()
	if err != nil || ttl <= 0 {
		t.Fatalf("the lease has no expiry (%v, %v); a dead holder would hold it forever", ttl, err)
	}

	// Nothing renews it, which is what a killed process looks like.
	successor := newBeat(t, rdb, cfg, "beat-successor")
	deadline := time.Now().Add(5 * time.Second)
	for {
		held, err := successor.lease.take(ctx)
		if err != nil {
			t.Fatalf("taking the lease: %v", err)
		}
		if held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the lease never expired, so no successor could ever beat")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := successor.lease.release(ctx); err != nil {
		t.Errorf("release: %v", err)
	}
}

// A lease released by its holder must not be releasable by anyone else, or one
// beat's clean stop would hand the schedule to two successors.
func TestOnlyTheHolderReleasesTheLease(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	clock := &testClock{at: time.Date(2026, 11, 8, 13, 47, 0, 0, time.UTC)}

	holder := newBeat(t, rdb, probeConfig(clock), "beat-holder")
	if held, _ := holder.lease.take(ctx); !held {
		t.Fatal("the lease should have been free")
	}
	other := newBeat(t, rdb, probeConfig(clock), "beat-other")
	if held, _ := other.lease.take(ctx); held {
		t.Fatal("two beats took the same lease")
	}
	if err := other.lease.release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	if held, _ := other.lease.take(ctx); held {
		t.Fatal("a beat released a lease it did not hold")
	}
	if err := holder.lease.release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	if held, _ := other.lease.take(ctx); !held {
		t.Fatal("the lease was not free after its holder released it")
	}
	_ = other.lease.release(ctx)
}

// Run on the real loop: it takes the lease, publishes on the schedule, and
// stops when it is asked to.
func TestRunPublishesOnTheScheduleAndStops(t *testing.T) {
	rdb := testRedis(t)
	clock := &testClock{at: time.Date(2026, 11, 8, 13, 47, 0, 0, time.UTC)}
	b := newBeat(t, rdb, probeConfig(clock), "beat-a")

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()

	waitFor(t, func() bool { return b.Snapshot().LeaseHeld }, "the beat never took the lease")
	clock.advance(time.Minute)
	waitFor(t, func() bool { return len(queued(t, rdb, probeQueue)) == 1 }, "the beat never published a tick")
	clock.advance(time.Minute)
	waitFor(t, func() bool { return len(queued(t, rdb, probeQueue)) == 2 }, "the beat published only once")

	stop()
	if err := <-done; err != nil {
		t.Errorf("Run: %v", err)
	}
	if b.Snapshot().LeaseHeld {
		t.Error("the lease should be given up when the beat stops")
	}
	if held, err := rdb.Exists(context.Background(), b.lease.key).Result(); err != nil || held != 0 {
		t.Error("a clean stop should release the lease rather than leave it to expire")
	}
}

func waitFor(t *testing.T, until func() bool, complaint string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !until() {
		if time.Now().After(deadline) {
			t.Fatal(complaint)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A schedule that cannot fire, or two entries with one name, is a schedule
// with a hole in it. Both are refused at startup.
func TestNewRefusesAScheduleItCannotRun(t *testing.T) {
	cases := map[string][]Entry{
		"no entries": {},
		"a duplicate name": {
			{Name: "same", Task: "a", Cron: Crontab{Minute: "*"}, Expires: time.Minute, Queue: probeQueue},
			{Name: "same", Task: "b", Cron: Crontab{Minute: "*"}, Expires: time.Minute, Queue: probeQueue},
		},
		"an uncompilable cron": {
			{Name: "bad", Task: "a", Cron: Crontab{Minute: "61"}, Expires: time.Minute, Queue: probeQueue},
		},
		"no queue": {
			{Name: "nowhere", Task: "a", Cron: Crontab{Minute: "*"}, Expires: time.Minute},
		},
		"no task": {
			{Name: "nothing", Cron: Crontab{Minute: "*"}, Expires: time.Minute, Queue: probeQueue},
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Entries = entries
			cfg.Name = "beat-bad"
			cfg.Origin = "gen1@probe"
			cfg.ReplyTo = "11111111-1111-4111-8111-111111111111"
			if _, err := New(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), cfg); err == nil {
				t.Fatal("expected an error, got none")
			}
		})
	}
}
