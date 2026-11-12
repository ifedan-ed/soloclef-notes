// Package automationtriggers starts automation runs: the tick that fires
// schedules that are due, and the reader of the event stream that fires
// event triggers.
//
// # The tick
//
// `automations.schedule_tick` is a message that schedules its own successor
// each interval through the broker's scheduled set, as the queued-job
// reconciler does (internal/jobreconcile): a worker seeds it as it starts,
// each run publishes the next one first, and a tick for a given moment is
// published only by whoever takes that moment's fence. So there is one tick
// an interval however many workers run, with no lease and no scheduler
// process.
//
// A tick does three things, each safe to do twice:
//
//  1. Fires the schedule triggers that are due. A due trigger is claimed by
//     advancing its next_fire_at under a row lock, so two ticks at once
//     cannot both fire it, and a fire that was missed (the workers were
//     down) is fired once, not once per missed moment.
//  2. Publishes again any run that has waited without being claimed: the
//     run's row is the outbox, and a message lost to a broker that was down
//     is found here. The claim makes a duplicate harmless.
//  3. Ends as timed out any run still "running" past its own time limit
//     plus a grace: its worker died, and nothing else would ever end it.
package automationtriggers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_automations/launch"
	"git.danvics.com/danvics/soloclef/soloclef_automations/runqueue"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

// Task is the tick's name.
const Task = runqueue.ScheduleTask

// Fire is one trigger that fired: whose automation, and what the run is
// told about the fire.
type Fire struct {
	TriggerID    int64
	AutomationID int64
	// UserID is the automation's owner, read with the trigger.
	UserID string
	// Inputs is the run's inputs, a JSON object.
	Inputs json.RawMessage
}

// Rows is the triggers and runs a tick reads (Store).
type Rows interface {
	// ClaimDue advances every schedule trigger that is due and returns the
	// ones whose automation is active, up to limit.
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]Fire, error)
	// StalePending is runs still pending after waited, oldest first.
	StalePending(ctx context.Context, waited time.Duration, limit int) ([]int64, error)
	// TimeOutOverdue ends runs still running past their limit plus grace.
	TimeOutOverdue(ctx context.Context, grace time.Duration) (int64, error)
	// Fired records that a trigger started a run.
	Fired(ctx context.Context, triggerID int64, at time.Time) error
	// AlreadyFired reports whether the trigger already has a run for the
	// event.
	AlreadyFired(ctx context.Context, triggerID int64, eventID string) (bool, error)
	// EventTriggers is the account's enabled event triggers for a type.
	EventTriggers(ctx context.Context, userID, eventType string) ([]EventTrigger, error)
	launch.Starter
}

// Sender publishes a message (produce.Producer).
type Sender interface {
	Send(ctx context.Context, message produce.Message) (string, error)
}

// Fence lets one tick be published for a moment (RedisFence).
type Fence interface {
	Take(ctx context.Context, moment time.Time, hold time.Duration) (bool, error)
	Release(ctx context.Context, moment time.Time) error
}

// Timing is how the tick paces itself: decisions made in cmd/worker.
type Timing struct {
	// Every is the interval between ticks: how late a schedule may fire.
	Every time.Duration
	// Batch bounds how many triggers, and how many runs, one tick handles.
	Batch int
	// RepublishAfter is how long a run may wait unclaimed before its
	// message is published again.
	RepublishAfter time.Duration
	// OverdueGrace is how far past its time limit a running run may be
	// before it is ended as timed out.
	OverdueGrace time.Duration
}

// Starter creates runs and publishes their messages. The tick and the event
// reader share it, so a schedule and an event start a run the same way.
type Starter struct {
	Rows      Rows
	Publisher Sender
	Log       *slog.Logger
}

// Start creates the fire's run, if its automation allows one now, and
// publishes it. It reports whether a run was created.
func (s Starter) Start(ctx context.Context, fire Fire) (bool, error) {
	runID, outcome, err := launch.Run(ctx, s.Rows, launch.Request{
		AutomationID: fire.AutomationID, UserID: fire.UserID, TriggerID: &fire.TriggerID, Inputs: fire.Inputs,
	})
	if err != nil {
		return false, err
	}
	if outcome != launch.Started {
		// Paused since the trigger was read, deleted, or already running
		// and set to skip: each is the automation's own setting at work.
		s.Log.Info("a trigger fired and no run was started", "trigger", fire.TriggerID, "automation", fire.AutomationID, "outcome", int(outcome))
		return false, nil
	}
	if _, err := s.Publisher.Send(ctx, runqueue.Execute(runID, 0)); err != nil {
		// The run exists; the tick's second duty publishes it.
		s.Log.Warn("a run's message was not published; the tick will publish it", "run", runID, "error", err.Error())
	}
	return true, nil
}

// Tick is the schedule tick.
type Tick struct {
	Starter Starter
	Fence   Fence
	Timing  Timing
	Now     func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (t *Tick) Check() error {
	switch {
	case t.Starter.Rows == nil || t.Starter.Publisher == nil || t.Starter.Log == nil || t.Fence == nil || t.Now == nil:
		return errors.New("automationtriggers: a dependency is missing")
	case t.Timing.Every <= 0 || t.Timing.Batch < 1 || t.Timing.OverdueGrace <= 0:
		return errors.New("automationtriggers: the timing is incomplete")
	case t.Timing.RepublishAfter < t.Timing.Every:
		// A run published this tick must not look lost to the next one
		// before a worker has had an interval to take it.
		return errors.New("automationtriggers: a run would be republished before one interval had passed")
	}
	return nil
}

// Run is the jobs.Handler: schedule the next tick, then do this one's work.
func (t *Tick) Run(ctx context.Context, _ *celery.Message) error {
	// The next tick first, so the chain never depends on this run's work.
	scheduleErr := t.Seed(ctx)
	workErr := t.Work(ctx)
	if scheduleErr != nil {
		// Without a next tick the chain would end here; the message goes
		// back and this run happens again, which is harmless.
		return fmt.Errorf("%w: scheduling the next automation tick: %w", broker.ErrRequeue, scheduleErr)
	}
	return workErr
}

// Seed publishes the tick for the next whole interval unless one is already
// on its way. A worker calls it once as it starts.
func (t *Tick) Seed(ctx context.Context) error {
	now := t.Now()
	next := now.Truncate(t.Timing.Every).Add(t.Timing.Every)
	// Held past the moment itself, so every caller that could ask for this
	// moment (they all ask before it comes) finds it taken.
	won, err := t.Fence.Take(ctx, next, 2*t.Timing.Every)
	if err != nil {
		return fmt.Errorf("taking the fence for %s: %w", next.UTC().Format(time.RFC3339), err)
	}
	if !won {
		return nil
	}
	_, err = t.Starter.Publisher.Send(ctx, produce.Message{
		Task: Task, ID: Task + ":" + strconv.FormatInt(next.Unix(), 10),
		Countdown: next.Sub(now), IgnoreResult: true,
		// No expiry: a tick that is dropped ends the chain; one that waited
		// out a long stop is run late, once.
	})
	if err != nil {
		if releaseErr := t.Fence.Release(context.WithoutCancel(ctx), next); releaseErr != nil {
			return fmt.Errorf("publishing the tick: %w (and its fence could not be released: %v)", err, releaseErr)
		}
		return fmt.Errorf("publishing the tick: %w", err)
	}
	return nil
}

// Work does one tick's three duties. One that fails does not stop the
// others; what failed is returned.
func (t *Tick) Work(ctx context.Context) error {
	var failures []error
	now, log := t.Now(), t.Starter.Log
	fires, err := t.Starter.Rows.ClaimDue(ctx, now, t.Timing.Batch)
	if err != nil {
		failures = append(failures, fmt.Errorf("claiming the due schedules: %w", err))
	}
	for _, fire := range fires {
		// The trigger is already advanced: a fire that cannot start its run
		// is missed and said, not retried into a second run later.
		if _, err := t.Starter.Start(ctx, fire); err != nil {
			failures = append(failures, fmt.Errorf("trigger %d: %w", fire.TriggerID, err))
		}
	}
	stale, err := t.Starter.Rows.StalePending(ctx, t.Timing.RepublishAfter, t.Timing.Batch)
	if err != nil {
		failures = append(failures, fmt.Errorf("reading the waiting runs: %w", err))
	}
	for _, runID := range stale {
		if _, err := t.Starter.Publisher.Send(ctx, runqueue.Execute(runID, 0)); err != nil {
			failures = append(failures, fmt.Errorf("run %d: %w", runID, err))
		}
	}
	ended, err := t.Starter.Rows.TimeOutOverdue(ctx, t.Timing.OverdueGrace)
	if err != nil {
		failures = append(failures, fmt.Errorf("ending the overdue runs: %w", err))
	}
	if len(fires) > 0 || len(stale) > 0 || ended > 0 {
		log.Info("an automation tick", "fired", len(fires), "republished", len(stale), "timed_out", ended)
	}
	return errors.Join(failures...)
}

// fencePrefix is the fence's keys: one a moment, each expiring by itself.
const fencePrefix = "soloclef:worker:automation-tick:"

// RedisFence is Fence in the broker's own Redis: where the ticks are, so a
// Redis that was emptied forgets the fences with the ticks they guarded.
type RedisFence struct {
	Redis redis.UniversalClient
}

func fenceKey(moment time.Time) string {
	return fencePrefix + strconv.FormatInt(moment.Unix(), 10)
}

func (f RedisFence) Take(ctx context.Context, moment time.Time, hold time.Duration) (bool, error) {
	return f.Redis.SetNX(ctx, fenceKey(moment), "1", hold).Result()
}

func (f RedisFence) Release(ctx context.Context, moment time.Time) error {
	return f.Redis.Del(ctx, fenceKey(moment)).Err()
}

// RedisBroker is produce.Broker over the worker's Redis client.
type RedisBroker struct {
	Redis redis.UniversalClient
}

func (b RedisBroker) LPush(ctx context.Context, key, value string) error {
	return b.Redis.LPush(ctx, key, value).Err()
}
