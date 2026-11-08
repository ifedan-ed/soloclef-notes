package executor

import (
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
)

// outcome is what this worker concludes about one task process, and the row it
// will write. err is what Run returns: nil only when the task succeeded.
type outcome struct {
	state     celery.State
	value     any
	traceback string
	err       error
}

// Exception types this worker decides on its own. The first four are Celery's
// own names, so a timed-out or lost job reads the same whichever worker ran
// it; the last is this worker's, because Celery has no name for a process that
// ran a body and then would not say what it did.
const (
	excSoftTimeLimit = "SoftTimeLimitExceeded"
	excHardTimeLimit = "TimeLimitExceeded"
	excWorkerLost    = "WorkerLostError"
	excTerminated    = "Terminated"
	excUnreadable    = "UnreadableTaskReport"
)

// workerFailure is a failure the worker observed rather than one the body
// reported, shaped so AsyncResult still reads it as an exception.
func workerFailure(excType string, err error) outcome {
	return outcome{
		state: celery.StateFailure,
		value: celery.ErrorValue(excType, err),
		err:   err,
	}
}

// softLimitFailure is the soft limit as Celery reports it: the body was asked
// to stop and the task ends failed. A body that caught the signal and returned
// anyway reports its own outcome, which wins over this one.
func softLimitFailure(limit time.Duration) outcome {
	return workerFailure(excSoftTimeLimit,
		fmt.Errorf("SoftTimeLimitExceeded(%v,)", limit))
}

func hardLimitFailure(limit time.Duration) outcome {
	return workerFailure(excHardTimeLimit,
		fmt.Errorf("TimeLimitExceeded(%v,)", limit))
}

// terminatedFailure is a job cut off because the worker is stopping. The row
// is written because this delivery will be acknowledged: leaving it STARTED
// would have clients polling a task nothing is running.
func terminatedFailure() outcome {
	return workerFailure(excTerminated,
		fmt.Errorf("the worker stopped while this task was running"))
}

// row is the result row for this outcome.
func (o outcome) row(msg *celery.Message, worker string) celery.Result {
	row := celery.ResultFor(msg, worker, o.state)
	row.Value = o.value
	row.Traceback = o.traceback
	row.DoneAt = time.Now().UTC()
	return row
}

// withLogs attaches what the process last said, so a row for a process that
// died without reporting still shows why.
func (o outcome) withLogs(tail string) outcome {
	if o.traceback == "" {
		o.traceback = tail
	}
	return o
}
