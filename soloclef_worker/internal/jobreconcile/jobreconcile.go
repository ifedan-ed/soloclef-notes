// Package jobreconcile republishes queued deliverable jobs whose message
// never reached the queue.
//
// # Why a queued job can have no message
//
// A job's row is committed before its message is published, by the chat
// that first queues it (soloclef_assistant/deliverables/video.go)
// and by the Retry button (soloclef_api/internal/deliverablejobs). The two
// are not one transaction and cannot be: one is Postgres, the other Redis.
// So a broker that is down, or an API that stops between the two, leaves a
// row that says "queued" with nothing on the queue to make it. Both
// producers log that and carry on, because the row is the outbox: this
// package is what reads it.
//
// # How a missed publish is recognised
//
// It is not recognised, exactly: nothing can tell a row whose message was
// lost from one whose message is waiting its turn on a busy queue. What is
// known is that a job has been queued, untouched, for longer than
// Timing.StaleAfter. Every such job is published again under the task id
// its row names. That is safe because of the worker's contract
// (internal/videojob): a job is claimed only for a message carrying its
// row's task id, and the claim is one locked transaction, so of any number
// of copies exactly one runs the attempt and the rest are acknowledged
// with nothing done.
//
// Each republish stamps the row's updated_at, so a job waiting behind a
// long queue costs one spare message every StaleAfter, not one a minute,
// and two ticks running at once cannot both publish the same job.
//
// Nothing here touches a running job. A worker that is stopping gives its
// attempt back to the queue, and one that died leaves a message the broker
// redelivers; the claim deals with both (videojob.Store.Claim).
//
// # How it is scheduled
//
// As a message, by the worker's own delayed delivery (internal/broker,
// scheduled.go), not by the beat. Each run publishes the next one first,
// with a countdown to the next whole interval: the consumer parks it in
// the scheduled set, where it costs no slot, and takes it when it is due.
// The chain is therefore as durable as any message: a worker that dies in
// the middle of a run leaves its message to be redelivered, and the run
// begins by scheduling again.
//
// A worker also publishes a tick when it starts (Seed), which is what
// starts the chain on a new deployment and restarts it if Redis was ever
// emptied. So that those seeds and the chain's own ticks do not add up,
// a tick for a given moment is published only by whoever takes that
// moment's fence (a key set once, in Redis): there is one tick an interval
// however many workers start, with no lease to hold and no scheduler
// process to keep single.
package jobreconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/videojob"
)

// Task is the tick's name. It is the name the Python tier's beat entry
// had, so a log search for it finds both.
const Task = "deliverables.reconcile_queued"

// Queued is a job that has waited too long: what to publish it as.
type Queued struct {
	ID int64
	// TaskID is the row's celery_task_id, the only id its claim accepts.
	// Empty means the row names none, and nothing could ever claim it.
	TaskID string
}

// Rows is the outbox (Store).
type Rows interface {
	// TakeStale returns up to limit jobs queued and untouched for at least
	// staleAfter, oldest first, and stamps each so that it is not returned
	// again until staleAfter has passed once more.
	TakeStale(ctx context.Context, staleAfter time.Duration, limit int) ([]Queued, error)
}

// Sender publishes a message (produce.Producer).
type Sender interface {
	Send(ctx context.Context, message produce.Message) (string, error)
}

// Fence lets one tick be published for a moment (RedisFence).
type Fence interface {
	// Take reports whether the caller is the first to ask for this moment.
	// The answer is remembered for hold.
	Take(ctx context.Context, moment time.Time, hold time.Duration) (bool, error)
	// Release gives a taken moment back, for a tick that could not be
	// published after all.
	Release(ctx context.Context, moment time.Time) error
}

// Timing is how the reconciler paces itself: decisions about the job, made
// in cmd/worker with their reasons.
type Timing struct {
	// Every is the interval between ticks.
	Every time.Duration
	// StaleAfter is how long a job may sit queued before it is published
	// again.
	StaleAfter time.Duration
	// Batch bounds one tick's work.
	Batch int
}

// Job runs the tick.
type Job struct {
	Rows      Rows
	Publisher Sender
	Fence     Fence
	Timing    Timing
	Log       *slog.Logger
	// Now is the clock; a test names another.
	Now func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (j *Job) Check() error {
	switch {
	case j.Rows == nil || j.Publisher == nil || j.Fence == nil || j.Log == nil || j.Now == nil:
		return errors.New("jobreconcile: a dependency is missing")
	case j.Timing.Every <= 0 || j.Timing.Batch < 1:
		return errors.New("jobreconcile: the timing is incomplete")
	case j.Timing.StaleAfter < j.Timing.Every:
		// A job published this tick must not look stale to the next one
		// before a worker has had an interval to take it.
		return errors.New("jobreconcile: a job would be republished before one interval had passed")
	}
	return nil
}

// Run is the jobs.Handler: schedule the next tick, then do this one's work.
func (j *Job) Run(ctx context.Context, _ *celery.Message) error {
	// The next tick first, so that the chain never depends on this run's
	// work succeeding, or finishing.
	scheduleErr := j.Seed(ctx)
	republished, err := j.Republish(ctx)
	if republished > 0 {
		j.Log.Info("queued deliverable jobs were published again", "jobs", republished)
	}
	if scheduleErr != nil {
		// Without a next tick the chain would end here. The message goes
		// back and this run happens again, which is harmless: the jobs it
		// took are stamped and are not taken twice.
		return fmt.Errorf("%w: scheduling the next reconciliation: %w", broker.ErrRequeue, scheduleErr)
	}
	return err
}

// Seed publishes the tick for the next whole interval, unless one is
// already on its way. A worker calls it once as it starts.
func (j *Job) Seed(ctx context.Context) error {
	now := j.Now()
	next := now.Truncate(j.Timing.Every).Add(j.Timing.Every)
	// Held past the moment itself: every other caller that could ask for
	// this moment asks before it comes, so two intervals covers them all
	// with room for clocks that disagree.
	won, err := j.Fence.Take(ctx, next, 2*j.Timing.Every)
	if err != nil {
		return fmt.Errorf("taking the fence for %s: %w", next.UTC().Format(time.RFC3339), err)
	}
	if !won {
		return nil
	}
	_, err = j.Publisher.Send(ctx, produce.Message{
		Task: Task,
		// One id a moment, so the log lines of a tick that was redelivered
		// read as the one tick it is.
		ID:        Task + ":" + strconv.FormatInt(next.Unix(), 10),
		Countdown: next.Sub(now),
		// Its outcome is the jobs it published; nobody reads a result.
		IgnoreResult: true,
		// No expiry, on purpose: a tick that is dropped ends the chain. One
		// that waited out a long stop is simply run late, once.
	})
	if err != nil {
		// The fence would otherwise keep everyone else from publishing
		// this moment's tick for as long as it is held.
		if releaseErr := j.Fence.Release(context.WithoutCancel(ctx), next); releaseErr != nil {
			return fmt.Errorf("publishing the tick: %w (and its fence could not be released: %v)", err, releaseErr)
		}
		return fmt.Errorf("publishing the tick: %w", err)
	}
	return nil
}

// Republish publishes every stale queued job again and returns how many it
// published.
func (j *Job) Republish(ctx context.Context) (int, error) {
	stale, err := j.Rows.TakeStale(ctx, j.Timing.StaleAfter, j.Timing.Batch)
	if err != nil {
		return 0, fmt.Errorf("reading the queued jobs: %w", err)
	}
	published := 0
	var failures []error
	for _, job := range stale {
		if job.TaskID == "" {
			// Both producers write the id in the statement that queues the
			// job, so this is a row written by something else. It is said,
			// every StaleAfter, rather than published under an id invented
			// here that its claim would refuse.
			j.Log.Error("a queued deliverable job names no task id, so no message can claim it", "job", job.ID)
			continue
		}
		if _, err := j.Publisher.Send(ctx, videojob.Message(job.ID, job.TaskID)); err != nil {
			// The row stays queued and is taken again after StaleAfter. One
			// job that cannot be published does not stop the others.
			failures = append(failures, fmt.Errorf("job %d: %w", job.ID, err))
			continue
		}
		published++
	}
	return published, errors.Join(failures...)
}
