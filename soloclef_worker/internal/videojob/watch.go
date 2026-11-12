package videojob

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// watch follows the row while the attempt runs. A cancel, a retry or a
// deleted job stops the work within one status interval, instead of at the
// next phase, which can be ten minutes of rendering away. And every
// keep-alive interval it records that the worker lives, which is how a
// redelivered message tells a slow render from a dead worker.
//
// The Python watcher asked twice a second; once every couple of seconds
// stops a render as promptly, at a quarter of the queries.
func (j *Job) watch(ctx context.Context, log *slog.Logger, job Claimed, stop context.CancelCauseFunc) {
	ticker := time.NewTicker(j.Timing.StatusInterval)
	defer ticker.Stop()
	touched := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		status, taskID, err := j.Rows.State(ctx, job)
		switch {
		case errors.Is(err, ErrGone):
			stop(errSuperseded)
			return
		case err != nil:
			if ctx.Err() == nil {
				log.Warn("the job's status could not be read; carrying on", "error", err.Error())
			}
			continue
		case taskID != job.TaskID:
			stop(errSuperseded)
			return
		case status == "cancelling" || status == "cancelled":
			stop(errCancelled)
			return
		case status != "running":
			stop(errSuperseded)
			return
		}
		if time.Since(touched) >= j.Timing.KeepAlive {
			// Set before the write: a failing database is tried at the
			// keep-alive's pace, not every status interval.
			touched = time.Now()
			if err := j.Rows.Touch(ctx, job); err != nil && ctx.Err() == nil {
				log.Warn("the keep-alive could not be recorded", "error", err.Error())
			}
		}
	}
}
