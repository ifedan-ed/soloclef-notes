package broker

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Settings Celery already enforces on these queues (app/celery_app.py). They
// are constants here because this worker has to live inside them, not choose
// them: the Python client is still the producer.
const (
	// KombuVisibilityWindow is the window a Celery worker on the Redis
	// transport gives itself before it treats another worker's in-flight
	// message as lost (kombu's Channel.visibility_timeout default, which
	// nothing in app/celery_app.py overrides).
	KombuVisibilityWindow = 3600 * time.Second

	// CeleryHardTimeLimit is task_time_limit: the longest a job may run.
	CeleryHardTimeLimit = 28800 * time.Second

	// CelerySoftTimeLimit is task_soft_time_limit: when a job is asked to
	// stop, ten minutes before it is made to.
	CelerySoftTimeLimit = 28200 * time.Second
)

var (
	// ErrNoQueues is the safe default made loud: a worker that was not told
	// which queues to take takes none.
	ErrNoQueues = errors.New("broker: no queues configured")

	// ErrNoQueueLimit refuses a queue with no concurrency limit rather than
	// inventing one.
	ErrNoQueueLimit = errors.New("broker: queue has no concurrency limit")

	// ErrQueueLimitBelowClassSum refuses limits that cannot all hold: a queue
	// smaller than the classes routed to it is a limit that never applies.
	ErrQueueLimitBelowClassSum = errors.New("broker: queue limit below the sum of the task classes it carries")

	// ErrVisibilityWindowTooShort refuses a window that would let this worker
	// reclaim a message a live Celery worker is still running.
	ErrVisibilityWindowTooShort = errors.New("broker: visibility window shorter than Celery's own")

	// ErrPopTimeoutTooShort refuses a BRPOP block Redis cannot express, which
	// would silently become one second.
	ErrPopTimeoutTooShort = errors.New("broker: pop timeout below BRPOP's one-second resolution")
)

// Config is everything the consumer needs to know. Nothing is inferred from
// the broker: a queue is consumed only because it is named here.
type Config struct {
	// Queues this worker consumes, in the order they are first polled.
	// Empty is the default and means consume nothing.
	Queues []string

	// QueueConcurrency caps how many of a queue's messages run at once. Every
	// queue in Queues must appear here.
	QueueConcurrency map[string]int

	// TaskConcurrency caps one task name across all queues. A task absent
	// here is bounded only by its queue, which is why the queue limit should
	// be at least the sum of the class limits the queue carries.
	TaskConcurrency map[string]int

	// TaskQueue names the queue each limited task class is routed to
	// (task_routes in app/celery_app.py), which is what makes the rule above
	// checkable rather than a note. A class with a limit but no entry here is
	// taken as carried by whichever queue delivers it.
	TaskQueue map[string]string

	// VisibilityWindow is how long an unacked message stays this worker's
	// before any worker may restore it. It must be at least
	// KombuVisibilityWindow, because a Python worker uses that number for
	// messages this worker holds as readily as for its own.
	VisibilityWindow time.Duration

	// RenewInterval is how often an in-flight message's claim is refreshed, so
	// a job legitimately running for hours is never mistaken for a lost one.
	RenewInterval time.Duration

	// RestoreInterval is how often this worker looks for messages whose claim
	// went stale — the only thing that makes a message a dead worker took
	// visible again.
	RestoreInterval time.Duration

	// PopTimeout is the BRPOP block, which also bounds how quickly a shutdown
	// or a freed slot is noticed. BRPOP's timeout is whole seconds, so this
	// must be at least one — kombu's brpop_timeout is 1 for the same reason.
	PopTimeout time.Duration

	// DeferredRetryDelay is how long a queue is left alone after a message was
	// put back because its task class was full, so a full class cannot spin.
	DeferredRetryDelay time.Duration

	// ETAHoldHorizon is how far ahead an eta may be and still be held in
	// memory until it is due. Anything further off goes back on the queue.
	// Celery's own countdowns here are seconds (app/notebooks/autoname,
	// app/tasks/celery_tasks/deliverable_job_tasks).
	ETAHoldHorizon time.Duration

	// ShutdownGrace is how long in-flight work is given to finish before the
	// worker stops waiting for it — a backstop for a deploy that reached this
	// process while it was working, not a way to drain one.
	//
	// It cannot be a drain strategy. A podcast, a video or a deliverable runs
	// for minutes to hours, so no grace this process could wait out would see
	// one finish: at the end of it the job is killed and written FAILURE, with
	// its billing hold held until the stale sweep, its row left GENERATING and
	// its sandbox still rendering for nobody. Draining is the deploy gate's
	// job, and it is required rather than conventional:
	//
	//	python3 soloclef_backend/scripts/deploy_gate.py --wait
	//
	// (see custom/docs/go-worker.md, "Restarting this worker"). The grace is
	// what bounds the damage when the gate was skipped or failed.
	ShutdownGrace time.Duration

	// Name identifies this worker in the result rows it writes.
	Name string
}

// DefaultConfig is every setting but the queues, which default to none.
func DefaultConfig() Config {
	return Config{
		Queues:           nil,
		QueueConcurrency: map[string]int{},
		TaskConcurrency:  map[string]int{},
		TaskQueue:        map[string]string{},

		// One hour longer than the 8h hard limit, so a job that runs to its
		// limit is never restored under itself.
		VisibilityWindow:   CeleryHardTimeLimit + time.Hour,
		RenewInterval:      5 * time.Minute,
		RestoreInterval:    100 * time.Second,
		PopTimeout:         time.Second,
		DeferredRetryDelay: time.Second,
		ETAHoldHorizon:     5 * time.Minute,
		// Thirty seconds is long enough for a job that was about to finish
		// and short enough that a stop ends. It is deliberately nowhere near
		// long enough to drain this tier's work: see ShutdownGrace above, and
		// run the deploy gate before every restart.
		ShutdownGrace: 30 * time.Second,
	}
}

// Validate refuses a configuration that would consume unsafely rather than
// correcting it.
func (c Config) Validate() error {
	if len(c.Queues) == 0 {
		return ErrNoQueues
	}
	for _, queue := range c.Queues {
		if c.QueueConcurrency[queue] < 1 {
			return fmt.Errorf("%w: %s", ErrNoQueueLimit, queue)
		}
	}
	for task, limit := range c.TaskConcurrency {
		if limit < 1 {
			return fmt.Errorf("broker: task %s has concurrency %d", task, limit)
		}
	}
	if err := c.validateQueueSizing(); err != nil {
		return err
	}
	if c.VisibilityWindow < KombuVisibilityWindow {
		return fmt.Errorf("%w: %v < %v", ErrVisibilityWindowTooShort, c.VisibilityWindow, KombuVisibilityWindow)
	}
	if c.RenewInterval <= 0 || c.RenewInterval >= c.VisibilityWindow {
		return fmt.Errorf("broker: renew interval %v does not fit the %v window", c.RenewInterval, c.VisibilityWindow)
	}
	if c.RestoreInterval <= 0 {
		return errors.New("broker: restore interval must be positive")
	}
	if c.PopTimeout < time.Second {
		return fmt.Errorf("%w: %v", ErrPopTimeoutTooShort, c.PopTimeout)
	}
	return nil
}

// validateQueueSizing checks each queue against the classes routed to it. The
// sum, not the maximum: the queue slot is taken before the class slot, so a
// queue that cannot hold every class at its limit is a queue where one class
// can shut the others out.
func (c Config) validateQueueSizing() error {
	carried := map[string]int{}
	for task, queue := range c.TaskQueue {
		limit, limited := c.TaskConcurrency[task]
		if !limited {
			continue
		}
		if !slices.Contains(c.Queues, queue) {
			return fmt.Errorf("broker: task %s is routed to %s, which this worker does not consume", task, queue)
		}
		carried[queue] += limit
	}
	for queue, sum := range carried {
		if limit := c.QueueConcurrency[queue]; limit < sum {
			return fmt.Errorf("%w: %s allows %d but carries classes summing to %d",
				ErrQueueLimitBelowClassSum, queue, limit, sum)
		}
	}
	return nil
}
