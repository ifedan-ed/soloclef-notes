// Package runqueue is the messages that start automation work, so the API
// tier (Run now) and the worker (a schedule or an event firing) publish the
// same message for the one job that reads it.
package runqueue

import (
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// ExecuteTask runs one run: the row `automation_runs.id` names.
const ExecuteTask = "automations.execute_run"

// ScheduleTask is the tick that fires due schedule triggers.
const ScheduleTask = "automations.schedule_tick"

// RunKey is ExecuteTask's one keyword argument.
const RunKey = "run_id"

// Execute is the message that runs a run. after delays it: zero for a run
// that should start now.
//
// The message carries the run's id and nothing else. Whose run it is, what
// it runs and whether it may still run are read from its row by the worker
// that claims it, so a message that is redelivered, duplicated or forged
// can start nothing its row does not already allow.
func Execute(runID int64, after time.Duration) produce.Message {
	return produce.Message{
		Task: ExecuteTask, Kwargs: map[string]any{RunKey: runID},
		// One id per run, so a redelivered message's log lines read as the
		// one run it is.
		ID:          ExecuteTask + ":" + strconv.FormatInt(runID, 10) + ":" + strconv.FormatInt(time.Now().UnixNano(), 36),
		ArgsAsTuple: true, Countdown: after,
		// The run's outcome is its row; nobody reads a result.
		IgnoreResult: true,
	}
}
