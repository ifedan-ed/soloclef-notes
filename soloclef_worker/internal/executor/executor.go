// Package executor runs Soloclef's existing Python task bodies as processes
// and reports what happened where Celery's clients read it. It is the half of
// the tier that stays Python: Go owns scheduling, limits, cancellation and the
// result rows, while the body itself is the same function a Celery worker
// would have called (surfsense_backend/app/tasks/run.py).
package executor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/celery"
)

const (
	// rowWriteTimeout bounds a write to the result backend. A row nobody can
	// write is logged; the job itself is what matters.
	rowWriteTimeout = 10 * time.Second

	// killConfirmGrace is how long after SIGKILL a process group is still
	// given to disappear before the task is reported without it.
	killConfirmGrace = 5 * time.Second
)

// Rows is the result backend, writing only (internal/results).
type Rows interface {
	Write(ctx context.Context, row celery.Result) error
}

// Executor is a broker.Runner: one message in, one task process run, one
// terminal row out.
type Executor struct {
	cfg  Config
	rows Rows
	log  *slog.Logger
}

// New refuses a configuration that could not run a body or report on one.
func New(cfg Config, rows Rows) (*Executor, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, errors.New("executor: no result backend, so nothing could read a task's outcome")
	}
	return &Executor{cfg: cfg, rows: rows, log: slog.Default().With("worker", cfg.Worker)}, nil
}

// Run runs one task body to its end. Only a message that was never run comes
// back: anything the body did, including failing, is finished work.
func (e *Executor) Run(ctx context.Context, msg *celery.Message) error {
	lim := e.cfg.limitsFor(msg)
	log := e.log.With("task", msg.Task, "id", msg.ID)

	process, err := startChild(e.cfg, msg.Task, callFor(msg, e.cfg.Worker, lim), log)
	if err != nil {
		// The body never ran, so the message is still work: hand it back
		// rather than turn a worker-side fault into a failed job. The wait is
		// what keeps a worker that can start nothing from spinning the queue.
		log.Error("could not start a task process", "error", err)
		e.waitBeforeHandingBack(ctx)
		return fmt.Errorf("%w: %w", broker.ErrRequeue, err)
	}

	// task_track_started is on, so the row goes out as soon as the body is
	// really running -- after the start rather than before it, because a
	// STARTED row for a process that never started would be a lie.
	e.write(ctx, msg, startedRow(msg, e.cfg.Worker, process.pid()), log)

	out := e.supervise(ctx, process, lim, log)
	e.write(ctx, msg, out.row(msg, e.cfg.Worker), log)
	return out.err
}

// supervise watches one process against its deadlines and the worker's own
// lifetime, and decides what the task's outcome was.
func (e *Executor) supervise(ctx context.Context, process *child, lim limits, log *slog.Logger) outcome {
	// A nil channel never fires, which is how a message with no soft limit
	// runs with no soft limit.
	var softLimit <-chan time.Time
	if lim.soft > 0 {
		timer := time.NewTimer(lim.soft)
		defer timer.Stop()
		softLimit = timer.C
	}
	hardLimit := time.NewTimer(lim.hard)
	defer hardLimit.Stop()

	var decided *outcome
	for {
		select {
		case <-process.done:
			return process.conclude(decided)

		case <-softLimit:
			log.Warn("the soft time limit is up; asking the task to stop", "limit", lim.soft)
			if err := raiseSoftLimit(process.pid()); err != nil {
				log.Error("could not raise the soft time limit inside the task", "error", err)
			}
			failure := softLimitFailure(lim.soft)
			decided = &failure

		case <-hardLimit.C:
			log.Error("the hard time limit is up; stopping the task's process group", "limit", lim.hard)
			process.stop(e.cfg.KillGrace)
			return e.awaitEnd(process, hardLimitFailure(lim.hard), log)

		case <-ctx.Done():
			log.Warn("the worker is stopping; stopping the task's process group")
			process.stop(e.cfg.KillGrace)
			return e.awaitEnd(process, terminatedFailure(), log)
		}
	}
}

// awaitEnd waits for a process that has been told to stop. A merely stubborn
// process group ends at the SIGKILL; one stuck in the kernel is reported
// without being waited out, because the client is owed an answer either way.
func (e *Executor) awaitEnd(process *child, decided outcome, log *slog.Logger) outcome {
	limit := time.NewTimer(e.cfg.KillGrace + killConfirmGrace + drainGrace)
	defer limit.Stop()
	select {
	case <-process.done:
		return process.conclude(&decided)
	case <-limit.C:
		log.Error("a killed task process has not ended; reporting the task without it", "pid", process.pid())
		return decided
	}
}

// write puts one row where Python's AsyncResult reads it.
func (e *Executor) write(ctx context.Context, msg *celery.Message, row celery.Result, log *slog.Logger) {
	if msg.IgnoreResult {
		// Celery stores nothing at all for a task declared ignore_result, so
		// a row here would be a state Python would never have shown.
		return
	}
	// Deliberately not ctx: a worker that is shutting down still owes its
	// clients the terminal row for the job it just cut off.
	writeCtx, done := context.WithTimeout(context.WithoutCancel(ctx), rowWriteTimeout)
	defer done()
	if err := e.rows.Write(writeCtx, row); err != nil {
		log.Error("could not write a result row", "state", row.State, "error", err)
	}
}

func (e *Executor) waitBeforeHandingBack(ctx context.Context) {
	timer := time.NewTimer(e.cfg.StartFailureDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// startedRow names the process running the task, as Celery's STARTED row does.
func startedRow(msg *celery.Message, worker string, pid int) celery.Result {
	row := celery.ResultFor(msg, worker, celery.StateStarted)
	row.Value = map[string]any{"hostname": worker, "pid": pid}
	return row
}
