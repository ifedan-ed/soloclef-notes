package broker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/queues"

	"git.danvics.com/danvics/soloclef/soloclef_celery/monotonic"
)

// Runner runs one task body. Phase 3 implements it; phase 2 only decides when
// a message reaches it, and what happens to the message afterwards.
//
// An error means the job finished badly, which under acks_late is still
// finished: the message is acknowledged and the failure is the result. Only
// ErrRequeue, and only a worker that dies, put a message back.
type Runner interface {
	Run(ctx context.Context, msg *celery.Message) error
}

var (
	// ErrRequeue asks for the message to go back on its queue instead of
	// being acknowledged.
	ErrRequeue = errors.New("broker: put this message back")

	// ErrShutdownIncomplete means the grace period ended with work still
	// running. Those messages keep their claims and so become visible again
	// once the visibility window passes, which is what
	// task_reject_on_worker_lost asks for.
	ErrShutdownIncomplete = errors.New("broker: shut down with work still running")
)

// Consumer takes messages off the queues it was configured with and hands them
// to a Runner, keeping Celery's own bookkeeping as it goes.
type Consumer struct {
	cfg      Config
	rdb      redis.UniversalClient
	runner   Runner
	registry *Registry
	slots    *slots
	claims   *claimSet
	metrics  *metrics
	cycle    *queueCycle
	clock    Clock
	log      *slog.Logger

	running       sync.WaitGroup
	waitingForETA atomic.Int64

	// coolUntil holds back a queue whose last message was put back, so a full
	// task class cannot be polled in a loop.
	coolMu    sync.Mutex
	coolUntil map[string]time.Time
}

// NewConsumer refuses a configuration that would consume unsafely — above all
// one that names no queues, which is the default.
func NewConsumer(rdb redis.UniversalClient, cfg Config, runner Runner) (*Consumer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, errors.New("broker: no runner")
	}
	return &Consumer{
		cfg:       cfg,
		rdb:       rdb,
		runner:    runner,
		registry:  NewRegistry(rdb, CeleryRegistryKeys(), cfg.VisibilityWindow),
		slots:     newSlots(cfg.QueueConcurrency, cfg.TaskConcurrency),
		claims:    newClaimSet(),
		metrics:   newMetrics(),
		cycle:     newQueueCycle(cfg.Queues),
		clock:     monotonic.Read,
		log:       slog.Default().With("worker", cfg.Name),
		coolUntil: map[string]time.Time{},
	}, nil
}

// Run consumes until ctx is cancelled, then stops taking new messages and
// gives the running ones ShutdownGrace to finish.
func (c *Consumer) Run(ctx context.Context) error {
	// The runner's context outlives the shutdown signal: a graceful stop is
	// the point at which no new work is taken, not the point at which running
	// work is cut off.
	runCtx, endRun := context.WithCancel(context.WithoutCancel(ctx))
	defer endRun()

	background, stopBackground := context.WithCancel(context.WithoutCancel(ctx))
	var background0 sync.WaitGroup
	background0.Add(2)
	go func() { defer background0.Done(); c.renewClaims(background) }()
	go func() { defer background0.Done(); c.restoreLost(background) }()

	c.poll(ctx, runCtx)

	drained := c.drain(c.cfg.ShutdownGrace)
	// Claims are renewed until the grace period is over, so a job that
	// finishes inside it is never restored under itself.
	stopBackground()
	background0.Wait()
	endRun()

	if !drained {
		return fmt.Errorf("%w: %d still running, their claims left for the %v visibility window",
			ErrShutdownIncomplete, c.claims.size(), c.cfg.VisibilityWindow)
	}
	return nil
}

// Snapshot reads the metrics worth having, including a live queue depth.
func (c *Consumer) Snapshot(ctx context.Context) (Snapshot, error) {
	byQueue, byTask := c.slots.inFlight()
	snapshot := Snapshot{
		InFlightByQueue: byQueue,
		InFlightByTask:  byTask,
		HeldClaims:      c.claims.size(),
		WaitingForETA:   int(c.waitingForETA.Load()),
	}
	c.metrics.fill(&snapshot)

	depths, err := Depths(ctx, c.rdb, c.cfg.Queues)
	if err != nil {
		return snapshot, err
	}
	snapshot.QueueDepth = depths
	return snapshot, nil
}

func (c *Consumer) poll(ctx, runCtx context.Context) {
	for ctx.Err() == nil {
		open := c.openQueues()
		if len(open) == 0 {
			c.waitForRoom(ctx)
			continue
		}
		msg, key, taken, err := c.take(ctx, open)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.log.Error("taking a message off the queue", "error", err)
			c.pause(ctx, c.cfg.DeferredRetryDelay)
			continue
		}
		if !taken {
			continue
		}
		c.dispatch(runCtx, msg, queues.QueueOf(key))
	}
}

// take blocks on the queues given until one has a message. The BRPOP itself
// is deliberately not cancellable: a cancelled pop can take a message off
// Redis and lose it, so shutdown is noticed between polls instead.
func (c *Consumer) take(ctx context.Context, wanted []string) (*celery.Message, string, bool, error) {
	popCtx, done := context.WithTimeout(context.WithoutCancel(ctx), c.cfg.PopTimeout+5*time.Second)
	defer done()

	popped, err := c.rdb.BRPop(popCtx, c.cfg.PopTimeout, popKeys(wanted)...).Result()
	if errors.Is(err, redis.Nil) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	if len(popped) != 2 {
		return nil, "", false, fmt.Errorf("broker: BRPOP returned %d values", len(popped))
	}
	key, raw := popped[0], popped[1]

	msg, err := celery.Decode([]byte(raw))
	if err != nil {
		// Nothing can run it and putting it back only means reading it again.
		c.metrics.count(&c.metrics.unreadable, 1)
		c.log.Error("dropping a message this worker cannot read", "queue", queues.QueueOf(key), "error", err)
		return nil, "", false, nil
	}
	c.metrics.count(&c.metrics.taken, 1)

	if err := c.registry.Claim(popCtx, msg); err != nil {
		// The message is off the list and unrecorded. Say so loudly: this is
		// the one window in which a crash loses work, and it is the window
		// Celery's own transport has too.
		c.log.Error("a message was taken but could not be claimed", "task", msg.Task, "id", msg.ID, "error", err)
		return nil, "", false, err
	}
	c.claims.add(msg.DeliveryTag)
	c.observeWait(msg)
	return msg, key, true, nil
}

// dispatch takes the queue slot and starts the job. The queue slot is taken
// here rather than before the pop because only this goroutine takes slots, so
// a queue that openQueues found open is still open now.
func (c *Consumer) dispatch(runCtx context.Context, msg *celery.Message, queue string) {
	due, expired := msg.Due(time.Now())
	if expired {
		c.metrics.count(&c.metrics.expired, 1)
		c.log.Info("dropping an expired message", "task", msg.Task, "id", msg.ID, "expires", msg.Expires)
		c.finish(msg, "", "")
		return
	}

	if !c.slots.takeQueue(queue) {
		// Cannot happen while one goroutine owns the slots, but putting the
		// message back is the only safe answer if it ever does.
		c.putBack(msg, queue, "")
		return
	}

	if !due {
		waitFor := time.Until(msg.ETA)
		if waitFor > c.cfg.ETAHoldHorizon {
			c.log.Info("putting back a message that is not due for a while",
				"task", msg.Task, "id", msg.ID, "eta", msg.ETA)
			c.putBack(msg, queue, "")
			c.coolDown(queue, c.cfg.DeferredRetryDelay)
			return
		}
	}

	c.running.Add(1)
	go func() {
		defer c.running.Done()
		c.hold(runCtx, msg, queue)
	}()
}

// hold waits for the message's eta if it has one, then runs it.
func (c *Consumer) hold(runCtx context.Context, msg *celery.Message, queue string) {
	if due, _ := msg.Due(time.Now()); !due {
		c.waitingForETA.Add(1)
		timer := time.NewTimer(time.Until(msg.ETA))
		defer timer.Stop()
		select {
		case <-timer.C:
			c.waitingForETA.Add(-1)
		case <-runCtx.Done():
			// It never started, so putting it back loses nothing.
			c.waitingForETA.Add(-1)
			c.putBack(msg, queue, "")
			return
		}
		if _, expired := msg.Due(time.Now()); expired {
			c.metrics.count(&c.metrics.expired, 1)
			c.finish(msg, queue, "")
			return
		}
	}

	if !c.slots.takeTask(msg.Task) {
		c.metrics.count(&c.metrics.deferred, 1)
		c.log.Info("putting back a message whose task class is full", "task", msg.Task, "id", msg.ID)
		c.putBack(msg, queue, "")
		c.coolDown(queue, c.cfg.DeferredRetryDelay)
		return
	}

	err := c.runner.Run(runCtx, msg)
	if errors.Is(err, ErrRequeue) {
		c.putBack(msg, queue, msg.Task)
		return
	}
	if err != nil {
		c.log.Warn("a job failed", "task", msg.Task, "id", msg.ID, "error", err)
	}
	// acks_late: the acknowledgement follows the job, not the delivery, and a
	// failure is as finished as a success.
	c.finish(msg, queue, msg.Task)
}

// finish acknowledges the message and frees its slots.
func (c *Consumer) finish(msg *celery.Message, queue, task string) {
	ackCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err := c.registry.Ack(ackCtx, msg.DeliveryTag); err != nil {
		// The claim stays, so the message will be restored and run again. That
		// is the acks_late bargain: at least once, never none.
		c.log.Error("could not acknowledge a finished job", "task", msg.Task, "id", msg.ID, "error", err)
	}
	c.claims.drop(msg.DeliveryTag)
	c.slots.release(queue, task)
}

// putBack returns a message to its queue, behind whatever is already waiting.
func (c *Consumer) putBack(msg *celery.Message, queue, task string) {
	backCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if _, err := c.registry.Requeue(backCtx, msg); err != nil {
		c.log.Error("could not put a message back", "task", msg.Task, "id", msg.ID, "error", err)
	} else {
		c.metrics.count(&c.metrics.requeued, 1)
	}
	c.claims.drop(msg.DeliveryTag)
	c.slots.release(queue, task)
}

func (c *Consumer) observeWait(msg *celery.Message) {
	waited, err := QueueWait(msg, c.clock)
	if err != nil {
		c.metrics.count(&c.metrics.waitUnknown, 1)
		return
	}
	c.metrics.observeWait(msg.Task, waited)
}

// openQueues is the queues with a free slot that are not cooling down.
func (c *Consumer) openQueues() []string {
	ready := make([]string, 0, len(c.cfg.Queues))
	now := time.Now()
	c.coolMu.Lock()
	for _, queue := range c.cfg.Queues {
		if until, cooling := c.coolUntil[queue]; cooling && now.Before(until) {
			continue
		}
		ready = append(ready, queue)
	}
	c.coolMu.Unlock()
	return c.cycle.only(c.slots.openQueues(ready))
}

func (c *Consumer) coolDown(queue string, until time.Duration) {
	c.coolMu.Lock()
	c.coolUntil[queue] = time.Now().Add(until)
	c.coolMu.Unlock()
}

// waitForRoom blocks until a slot frees up, a cooldown lapses, or the worker
// is asked to stop, so a worker at its limits does not poll Redis for nothing.
func (c *Consumer) waitForRoom(ctx context.Context) {
	timer := time.NewTimer(c.cfg.DeferredRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-c.slots.freed():
	case <-timer.C:
	}
}

func (c *Consumer) pause(ctx context.Context, howLong time.Duration) {
	timer := time.NewTimer(howLong)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// renewClaims keeps every held claim fresh. Without it the one-hour window
// kombu defaults to would restore this worker's own eight-hour jobs under it.
func (c *Consumer) renewClaims(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.RenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			held := c.claims.held()
			if len(held) == 0 {
				continue
			}
			if err := c.registry.Renew(ctx, held); err != nil {
				c.log.Error("could not renew claims", "held", len(held), "error", err)
			}
		}
	}
}

// restoreLost makes messages whose worker died visible again. Nothing else
// does: on Redis there is no broker to notice a consumer disappearing.
func (c *Consumer) restoreLost(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.RestoreInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			restored, err := c.registry.RestoreVisible(ctx)
			if err != nil {
				c.log.Error("could not restore lost messages", "error", err)
				continue
			}
			if restored > 0 {
				c.metrics.count(&c.metrics.restored, uint64(restored))
				c.log.Warn("made messages visible again after their claims went stale", "count", restored)
			}
		}
	}
}

// drain waits for the running jobs, and reports whether they all finished.
func (c *Consumer) drain(grace time.Duration) bool {
	finished := make(chan struct{})
	go func() {
		c.running.Wait()
		close(finished)
	}()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-finished:
		return true
	case <-timer.C:
		return false
	}
}
