// Package jobs is the worker tier's job table: which Go function runs which
// task name, and how long it may take. It is a broker.Runner, so the
// consumer hands it every message it takes off the default and heavy
// queues, the way it hands index_document messages to internal/indextask.
//
// # Why a table and not a process per job
//
// The job bodies used to be Python, run in a pool of warm interpreter
// processes (the removed internal/executor). Each of those bodies read
// workspace columns the new schema no longer has, so every one of them
// failed. They are Go now, keyed by the account that owns the rows, and run
// in this process as goroutines: a job costs what it does, not a 27-second
// import and the best part of a gigabyte.
//
// # What a message that names no job gets
//
// It is acknowledged and logged, never put back. Nothing in this process
// will ever be able to run it, and a message put back for ever would starve
// the queue it is on. A task name nothing registers is a producer that has
// not been brought over, which the log line names.
//
// # No result rows
//
// Celery wrote a result row for every task, because something polled
// AsyncResult for it. Nothing does any more: each job's outcome is in the
// row it is about (the podcast's status, the notebook's title, the
// document that is gone), which is what the apps already read. A second
// record of it could only disagree with the first.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

// Handler runs one message's job. A nil return or a plain error
// acknowledges the message; an error wrapping broker.ErrRequeue puts it
// back on its queue.
type Handler func(ctx context.Context, msg *celery.Message) error

// Job is one registered task.
type Job struct {
	// Task is the name the message carries.
	Task string
	// Deadline bounds one run. Every job states its own: how long a job
	// may hold a slot is a property of the job, and a single default would
	// be either too long for a naming call or too short for a podcast.
	Deadline time.Duration
	Run      Handler
}

// Table is a broker.Runner over the registered jobs.
type Table struct {
	jobs map[string]Job
	log  *slog.Logger
}

// New refuses an incomplete or repeated registration: two jobs under one
// name would make which one runs depend on the order of a slice.
func New(log *slog.Logger, jobs ...Job) (*Table, error) {
	if log == nil {
		return nil, errors.New("jobs: no logger")
	}
	table := &Table{jobs: make(map[string]Job, len(jobs)), log: log}
	for _, job := range jobs {
		switch {
		case job.Task == "":
			return nil, errors.New("jobs: a job has no task name")
		case job.Run == nil:
			return nil, fmt.Errorf("jobs: %s has no handler", job.Task)
		case job.Deadline <= 0:
			return nil, fmt.Errorf("jobs: %s has no deadline", job.Task)
		}
		if _, taken := table.jobs[job.Task]; taken {
			return nil, fmt.Errorf("jobs: %s is registered twice", job.Task)
		}
		table.jobs[job.Task] = job
	}
	return table, nil
}

// Tasks is every registered name, sorted, for the start-up log.
func (t *Table) Tasks() []string {
	names := make([]string, 0, len(t.jobs))
	for name := range t.jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ErrUnknownTask is a message whose task nothing here runs.
var ErrUnknownTask = errors.New("jobs: no job is registered for this task")

// Run is broker.Runner.
func (t *Table) Run(ctx context.Context, msg *celery.Message) error {
	job, known := t.jobs[msg.Task]
	if !known {
		// Acknowledged, not put back (see the package comment).
		return fmt.Errorf("%w: %q on queue %q", ErrUnknownTask, msg.Task, msg.Queue)
	}
	ctx, cancel := context.WithTimeout(ctx, job.Deadline)
	defer cancel()
	started := time.Now()
	err := job.Run(ctx, msg)
	took := time.Since(started).Round(time.Millisecond).String()
	// A failure is logged once, by the consumer, with the error. What only
	// this table knows is how long the job took, so it says that.
	switch {
	case err == nil:
		t.log.Info("job finished", "task", msg.Task, "id", msg.ID, "took", took)
	case errors.Is(err, broker.ErrRequeue):
		t.log.Warn("job handed back to its queue", "task", msg.Task, "id", msg.ID, "took", took, "error", err.Error())
	default:
		t.log.Info("job ended in failure", "task", msg.Task, "id", msg.ID, "took", took)
	}
	return err
}
