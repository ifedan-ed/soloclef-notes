package drivesync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivesyncjob"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

// # The schedule
//
// A Drive connection is synced every `indexing_frequency_minutes` (a day,
// as connecting sets it). `next_scheduled_at` on the row says when it is
// next due; the tick below publishes a sync for each connection that is,
// and moves the time on.
//
// The tick is a message that schedules its own successor through the
// worker's delayed delivery, exactly as the queued-job reconciler's is
// (internal/jobreconcile, "How it is scheduled", which gives the whole
// argument): no beat entry, no scheduler process to keep single, and a
// fence in Redis so that there is one tick an interval however many workers
// start.

// TickTask is the tick's name.
const TickTask = "google_drive.sync_due"

// Due is a connection whose sync is due.
type Due struct {
	ConnectorID int64
	UserID      string
}

// Schedule is the connections' schedule in the database (DueStore).
type Schedule interface {
	// TakeDue returns up to limit connections that are due, soonest first,
	// and moves each one's next time on by its own interval, so that it is
	// not returned again until then.
	TakeDue(ctx context.Context, limit int) ([]Due, error)
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

// Message is the sync of one connection, as a message: the one the API
// publishes for an import or a "sync now", field for field.
func Message(request drivesyncjob.Request) produce.Message {
	return produce.Message{
		Task: Task, Kwargs: request.Kwargs(),
		// Keyword arguments only: the (empty) positional arguments are
		// written as a tuple, as Celery's send_task writes them.
		ArgsAsTuple: true,
		// The outcome is the documents' own statuses; nobody reads a result.
		IgnoreResult: true,
	}
}

// Tick publishes the syncs that are due.
type Tick struct {
	Schedule  Schedule
	Publisher Sender
	Fence     Fence
	// Every is the interval between ticks: how late a due sync may start.
	Every time.Duration
	// Batch bounds one tick's work; what is left is the next tick's.
	Batch int
	Log   *slog.Logger
	// Now is the clock; a test names another.
	Now func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (t *Tick) Check() error {
	switch {
	case t.Schedule == nil || t.Publisher == nil || t.Fence == nil || t.Log == nil || t.Now == nil:
		return errors.New("drivesync: a dependency of the tick is missing")
	case t.Every <= 0 || t.Batch < 1:
		return errors.New("drivesync: the tick's timing is incomplete")
	}
	return nil
}

// Run is the jobs.Handler: schedule the next tick, then do this one's work.
func (t *Tick) Run(ctx context.Context, _ *celery.Message) error {
	// The next tick first, so that the chain never depends on this run's
	// work succeeding, or finishing.
	scheduleErr := t.Seed(ctx)
	published, err := t.PublishDue(ctx)
	if published > 0 {
		t.Log.Info("Drive syncs that were due have been queued", "connections", published)
	}
	if scheduleErr != nil {
		// Without a next tick the chain would end here. The message goes
		// back and this run happens again, which is harmless: the
		// connections it took have been moved on and are not taken twice.
		return fmt.Errorf("%w: scheduling the next Drive tick: %w", broker.ErrRequeue, scheduleErr)
	}
	return err
}

// Seed publishes the tick for the next whole interval, unless one is
// already on its way. A worker calls it once as it starts.
func (t *Tick) Seed(ctx context.Context) error {
	now := t.Now()
	next := now.Truncate(t.Every).Add(t.Every)
	// Held past the moment itself: every other caller that could ask for
	// this moment asks before it comes, so two intervals covers them all
	// with room for clocks that disagree.
	won, err := t.Fence.Take(ctx, next, 2*t.Every)
	if err != nil {
		return fmt.Errorf("taking the fence for %s: %w", next.UTC().Format(time.RFC3339), err)
	}
	if !won {
		return nil
	}
	_, err = t.Publisher.Send(ctx, produce.Message{
		Task: TickTask,
		// One id a moment, so the log lines of a tick that was redelivered
		// read as the one tick it is.
		ID:           TickTask + ":" + strconv.FormatInt(next.Unix(), 10),
		Countdown:    next.Sub(now),
		IgnoreResult: true,
		// No expiry, on purpose: a tick that is dropped ends the chain.
	})
	if err != nil {
		// The fence would otherwise keep everyone else from publishing
		// this moment's tick for as long as it is held.
		if releaseErr := t.Fence.Release(context.WithoutCancel(ctx), next); releaseErr != nil {
			return fmt.Errorf("publishing the tick: %w (and its fence could not be released: %v)", err, releaseErr)
		}
		return fmt.Errorf("publishing the tick: %w", err)
	}
	return nil
}

// PublishDue publishes a sync for every connection that is due and returns
// how many it published.
func (t *Tick) PublishDue(ctx context.Context) (int, error) {
	due, err := t.Schedule.TakeDue(ctx, t.Batch)
	if err != nil {
		return 0, fmt.Errorf("reading the Drive connections that are due: %w", err)
	}
	published := 0
	var failures []error
	for _, connection := range due {
		request := drivesyncjob.Request{ConnectorID: connection.ConnectorID, UserID: connection.UserID}
		if _, err := t.Publisher.Send(ctx, Message(request)); err != nil {
			// Its time has been moved on, so this connection waits one
			// interval of its own. That is said, not hidden; one
			// connection that cannot be published does not stop the rest.
			failures = append(failures, fmt.Errorf("connection %d: %w", connection.ConnectorID, err))
			continue
		}
		published++
	}
	return published, errors.Join(failures...)
}
