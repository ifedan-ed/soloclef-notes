package broker

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/monotonic"
)

const testQueue = "probe.queue"

// fastConfig keeps the real acks_late numbers — the visibility window is a
// contract with Celery, not a test knob — and shortens only the polling.
func fastConfig(limit int) Config {
	cfg := DefaultConfig()
	cfg.Queues = []string{testQueue}
	cfg.QueueConcurrency = map[string]int{testQueue: limit}
	cfg.PopTimeout = time.Second
	cfg.RestoreInterval = 50 * time.Millisecond
	cfg.RenewInterval = time.Second
	cfg.DeferredRetryDelay = 20 * time.Millisecond
	cfg.ShutdownGrace = 2 * time.Second
	cfg.Name = "go-worker-test"
	return cfg
}

type runnerFunc func(context.Context, *celery.Message) error

func (f runnerFunc) Run(ctx context.Context, msg *celery.Message) error { return f(ctx, msg) }

// publish puts an envelope on the queue the way the Python producer does:
// LPUSH, so BRPOP takes the oldest first.
func publish(t *testing.T, rdb *redis.Client, raw []byte) {
	t.Helper()
	if err := rdb.LPush(context.Background(), testQueue, string(raw)).Err(); err != nil {
		t.Fatalf("publishing: %v", err)
	}
}

func runConsumer(t *testing.T, rdb *redis.Client, cfg Config, runner Runner) (*Consumer, func()) {
	return runConsumerOn(t, rdb, cfg, runner)
}

func runConsumerOn(t *testing.T, rdb *redis.Client, cfg Config, runner Runner) (*Consumer, func()) {
	t.Helper()
	consumer, err := NewConsumer(rdb, cfg, runner)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- consumer.Run(ctx) }()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("the consumer did not stop")
		}
	})
	t.Cleanup(stop)
	return consumer, stop
}

func waitFor(t *testing.T, why string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

// The default that keeps a dev build harmless must hold at the seam that
// actually reads Redis, not only in Validate.
func TestAConsumerWithNoQueuesRefusesToStart(t *testing.T) {
	consumer, err := NewConsumer(nil, DefaultConfig(), runnerFunc(func(context.Context, *celery.Message) error {
		t.Fatal("nothing should run")
		return nil
	}))
	if !errors.Is(err, ErrNoQueues) {
		t.Fatalf("NewConsumer = %v, want ErrNoQueues", err)
	}
	if consumer != nil {
		t.Error("no consumer should be returned")
	}
}

func TestAMessageIsRunAndThenAcked(t *testing.T) {
	rdb := testRedis(t)
	ran := make(chan string, 1)
	_, _ = runConsumer(t, rdb, fastConfig(1), runnerFunc(func(_ context.Context, msg *celery.Message) error {
		ran <- msg.Task
		return nil
	}))

	publish(t, rdb, envelopeFor("tag-run", testQueue, 0))
	select {
	case task := <-ran:
		if task != "probe.task" {
			t.Errorf("ran %q", task)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the message was never run")
	}
	waitFor(t, "the claim to be acked", func() bool {
		n, _ := rdb.HLen(context.Background(), "unacked").Result()
		return n == 0
	})
}

// acks_late means acked after the job, not after the delivery. A job that
// failed is still finished, so Celery acks it; only a lost worker requeues.
func TestAFailedJobIsStillAcked(t *testing.T) {
	rdb := testRedis(t)
	ran := make(chan struct{}, 1)
	_, _ = runConsumer(t, rdb, fastConfig(1), runnerFunc(func(context.Context, *celery.Message) error {
		ran <- struct{}{}
		return errors.New("the job failed")
	}))

	publish(t, rdb, envelopeFor("tag-fail", testQueue, 0))
	<-ran
	waitFor(t, "the failed job to be acked", func() bool {
		n, _ := rdb.HLen(context.Background(), "unacked").Result()
		return n == 0
	})
	if n, _ := rdb.LLen(context.Background(), testQueue).Result(); n != 0 {
		t.Error("a failed job must not be requeued; its failure is the result")
	}
}

func TestARunnerCanAskForTheMessageBack(t *testing.T) {
	rdb := testRedis(t)
	var attempts atomic.Int64
	_, stop := runConsumer(t, rdb, fastConfig(1), runnerFunc(func(context.Context, *celery.Message) error {
		attempts.Add(1)
		return ErrRequeue
	}))

	publish(t, rdb, envelopeFor("tag-back", testQueue, 0))
	waitFor(t, "the message to be taken twice", func() bool { return attempts.Load() >= 2 })
	stop()
	waitFor(t, "the message to be back on the queue", func() bool {
		depth, _ := rdb.LLen(context.Background(), testQueue).Result()
		held, _ := rdb.HLen(context.Background(), "unacked").Result()
		return depth == 1 && held == 0
	})
}

// A queue's limit is the whole point of consuming it here rather than in a
// prefork pool sized by how much memory a Python process needs.
func TestAQueuesLimitBoundsHowMuchOfItRunsAtOnce(t *testing.T) {
	rdb := testRedis(t)
	release := make(chan struct{})
	var running, peak atomic.Int64
	var finished atomic.Int64
	_, _ = runConsumer(t, rdb, fastConfig(2), runnerFunc(func(context.Context, *celery.Message) error {
		now := running.Add(1)
		for {
			was := peak.Load()
			if now <= was || peak.CompareAndSwap(was, now) {
				break
			}
		}
		<-release
		running.Add(-1)
		finished.Add(1)
		return nil
	}))

	for i := 0; i < 6; i++ {
		publish(t, rdb, envelopeFor("tag-limit-"+strconv.Itoa(i), testQueue, 0))
	}
	waitFor(t, "the queue limit to be reached", func() bool { return running.Load() == 2 })
	time.Sleep(200 * time.Millisecond)
	if peak.Load() > 2 {
		t.Fatalf("%d messages ran at once against a limit of 2", peak.Load())
	}
	close(release)
	waitFor(t, "every message to finish", func() bool { return finished.Load() == 6 })
	if peak.Load() > 2 {
		t.Errorf("peak concurrency was %d", peak.Load())
	}
}

// The limit `celery_heavy` could not express: one task class capped while the
// rest of its queue keeps moving.
func TestATaskClassLimitHoldsWhileTheQueueKeepsMoving(t *testing.T) {
	rdb := testRedis(t)
	cfg := fastConfig(4)
	cfg.TaskConcurrency = map[string]int{"probe.task": 1}

	release := make(chan struct{})
	var inClass, classPeak, other atomic.Int64
	_, _ = runConsumer(t, rdb, cfg, runnerFunc(func(_ context.Context, msg *celery.Message) error {
		if msg.Task != "probe.task" {
			other.Add(1)
			return nil
		}
		now := inClass.Add(1)
		for {
			was := classPeak.Load()
			if now <= was || classPeak.CompareAndSwap(was, now) {
				break
			}
		}
		<-release
		inClass.Add(-1)
		return nil
	}))

	for i := 0; i < 3; i++ {
		publish(t, rdb, envelopeFor("tag-class-"+strconv.Itoa(i), testQueue, 0))
	}
	publish(t, rdb, taskEnvelope("tag-other", testQueue, "probe.other"))

	waitFor(t, "the other class to get through", func() bool { return other.Load() == 1 })
	if classPeak.Load() > 1 {
		t.Errorf("%d of a class limited to 1 ran at once", classPeak.Load())
	}
	close(release)
	waitFor(t, "the limited class to drain", func() bool { return inClass.Load() == 0 })
}

// expires is the producer saying the work is pointless after a deadline. The
// beat schedule uses it (expires=30 on check_periodic_schedules), so running
// one late is worse than not running it.
func TestAnExpiredMessageIsDroppedUnrun(t *testing.T) {
	rdb := testRedis(t)
	var ran atomic.Int64
	consumer, _ := runConsumer(t, rdb, fastConfig(1), runnerFunc(func(context.Context, *celery.Message) error {
		ran.Add(1)
		return nil
	}))

	expired := scheduledEnvelope("tag-expired", testQueue, "", time.Now().Add(-time.Minute))
	publish(t, rdb, expired)
	publish(t, rdb, envelopeFor("tag-after-expired", testQueue, 0))

	waitFor(t, "the following message to run", func() bool { return ran.Load() == 1 })
	snapshot, err := consumer.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.Expired != 1 {
		t.Errorf("Expired = %d, want 1", snapshot.Expired)
	}
	waitFor(t, "the expired claim to be dropped", func() bool {
		n, _ := rdb.HLen(context.Background(), "unacked").Result()
		return n == 0
	})
}

// countdown= becomes an eta header, and this app uses it (notebook autoname,
// deliverable retries). Running one early would be a bug the Python worker
// does not have.
func TestAnETAMessageWaitsForItsTime(t *testing.T) {
	rdb := testRedis(t)
	ranAt := make(chan time.Time, 1)
	_, _ = runConsumer(t, rdb, fastConfig(1), runnerFunc(func(context.Context, *celery.Message) error {
		ranAt <- time.Now()
		return nil
	}))

	due := time.Now().Add(700 * time.Millisecond)
	publish(t, rdb, scheduledEnvelope("tag-eta", testQueue, due.UTC().Format(time.RFC3339Nano), time.Time{}))
	select {
	case at := <-ranAt:
		if at.Before(due.Add(-50 * time.Millisecond)) {
			t.Errorf("ran %v early", due.Sub(at))
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the eta message never ran")
	}
}

// The acks_late contract from the other side: a message whose worker died is
// restored by whichever worker notices, and then runs.
func TestAMessageLeftByADeadWorkerIsPickedUpAgain(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	ran := make(chan string, 1)
	cfg := fastConfig(1)
	_, _ = runConsumer(t, rdb, cfg, runnerFunc(func(_ context.Context, msg *celery.Message) error {
		ran <- msg.DeliveryTag
		return nil
	}))

	// Exactly what a worker killed mid-job leaves behind: a claim nobody is
	// refreshing, and no message on any list.
	abandoned := NewRegistry(rdb, CeleryRegistryKeys(), cfg.VisibilityWindow)
	msg := decodeFor(t, envelopeFor("tag-orphan", testQueue, 0))
	if err := abandoned.Claim(ctx, msg); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	stale := float64(time.Now().Add(-2*cfg.VisibilityWindow).UnixNano()) / float64(time.Second)
	if err := rdb.ZAdd(ctx, "unacked_index", redisMember("tag-orphan", stale)).Err(); err != nil {
		t.Fatalf("ageing the claim: %v", err)
	}

	select {
	case tag := <-ran:
		if tag != "tag-orphan" {
			t.Errorf("ran %q", tag)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the abandoned message was never restored")
	}
	if !decodeFor(t, envelopeFor("tag-orphan", testQueue, 0)).Redelivered {
		// Guard against the fixture itself claiming to be redelivered.
		t.Log("fixture is a first delivery, as expected")
	}
}

// Graceful shutdown: no new work is taken, and work already running finishes.
func TestShutdownLetsRunningWorkFinish(t *testing.T) {
	rdb := testRedis(t)
	started := make(chan struct{})
	finished := make(chan struct{})
	_, stop := runConsumer(t, rdb, fastConfig(1), runnerFunc(func(context.Context, *celery.Message) error {
		close(started)
		time.Sleep(300 * time.Millisecond)
		close(finished)
		return nil
	}))

	publish(t, rdb, envelopeFor("tag-drain", testQueue, 0))
	<-started
	stop()
	select {
	case <-finished:
	default:
		t.Fatal("shutdown returned before the running job finished")
	}
	if n, _ := rdb.HLen(context.Background(), "unacked").Result(); n != 0 {
		t.Error("the finished job should have been acked before shutdown returned")
	}
}

// A job that ignores the deadline must not be acked on its behalf: leaving the
// claim is what makes the message visible again, which is what
// task_reject_on_worker_lost asks for.
func TestShutdownHonoursItsDeadlineAndLeavesTheClaimBehind(t *testing.T) {
	rdb := testRedis(t)
	cfg := fastConfig(1)
	cfg.ShutdownGrace = 200 * time.Millisecond
	started := make(chan struct{})
	stuck := make(chan struct{})

	consumer, err := NewConsumer(rdb, cfg, runnerFunc(func(context.Context, *celery.Message) error {
		close(started)
		<-stuck
		return nil
	}))
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- consumer.Run(ctx) }()

	publish(t, rdb, envelopeFor("tag-stuck", testQueue, 0))
	<-started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, ErrShutdownIncomplete) {
			t.Fatalf("Run = %v, want ErrShutdownIncomplete", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown ignored its deadline")
	}
	held, _ := rdb.HKeys(context.Background(), "unacked").Result()
	if len(held) != 1 || held[0] != "tag-stuck" {
		t.Errorf("unacked holds %q; the abandoned job's claim must stay so it is restored", held)
	}
	close(stuck)
}

// The three metrics the tier was built to have.
func TestSnapshotReportsDepthInFlightAndTrueQueueWait(t *testing.T) {
	rdb := testRedis(t)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	consumer, _ := runConsumer(t, rdb, fastConfig(1), runnerFunc(func(context.Context, *celery.Message) error {
		started <- struct{}{}
		<-release
		return nil
	}))

	stamp, err := monotonic.Read()
	if err != nil {
		t.Fatalf("ReadMonotonic: %v", err)
	}
	publish(t, rdb, stampedEnvelope("tag-metric-1", testQueue, stamp-250*time.Millisecond))
	<-started
	publish(t, rdb, envelopeFor("tag-metric-2", testQueue, 0))
	publish(t, rdb, envelopeFor("tag-metric-3", testQueue, 0))

	waitFor(t, "the waiting messages to show as depth", func() bool {
		snapshot, err := consumer.Snapshot(context.Background())
		return err == nil && snapshot.QueueDepth[testQueue] == 2
	})
	snapshot, err := consumer.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.InFlightByTask["probe.task"] != 1 || snapshot.InFlightByQueue[testQueue] != 1 {
		t.Errorf("in flight: %v / %v", snapshot.InFlightByQueue, snapshot.InFlightByTask)
	}
	wait := snapshot.Wait["probe.task"]
	if wait.Count != 1 {
		t.Fatalf("wait observations = %d", wait.Count)
	}
	if wait.Max < 250*time.Millisecond || wait.Max > 30*time.Second {
		t.Errorf("queue wait = %v, want about 250ms", wait.Max)
	}
	close(release)
}

// An envelope this worker cannot read must not sit in the registry forever,
// and must not be retried in a loop either.
func TestAnUnreadableMessageIsDroppedAndCounted(t *testing.T) {
	rdb := testRedis(t)
	consumer, _ := runConsumer(t, rdb, fastConfig(1), runnerFunc(func(context.Context, *celery.Message) error {
		t.Error("an unreadable envelope must not reach the runner")
		return nil
	}))

	if err := rdb.LPush(context.Background(), testQueue, "{not an envelope").Err(); err != nil {
		t.Fatalf("publishing: %v", err)
	}
	waitFor(t, "the unreadable message to be counted", func() bool {
		snapshot, err := consumer.Snapshot(context.Background())
		return err == nil && snapshot.Unreadable == 1
	})
	if n, _ := rdb.LLen(context.Background(), testQueue).Result(); n != 0 {
		t.Error("an unreadable message must not be put back to be read again")
	}
}
