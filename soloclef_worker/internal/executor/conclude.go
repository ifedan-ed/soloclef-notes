package executor

import (
	"errors"
	"fmt"
	"os"
)

// concludeFrom is the one decision about what a task's row says, shared by a
// process started for one job and a warm process that ran many: the body's own
// report wins whenever there is one, because a task that caught a soft limit
// and returned a value succeeded, here as in Celery.
//
// fallback is what the worker decided — a limit or a shutdown — and exit is
// how the process ended, which only matters when there is neither a report nor
// a decision.
func concludeFrom(report []byte, logs string, exit func() outcome, fallback *outcome) outcome {
	parsed, err := parseReport(report)
	switch {
	case err == nil:
		return parsed.outcome()
	case fallback != nil:
		return fallback.withLogs(logs)
	case !errors.Is(err, errNoReport):
		return workerFailure(excUnreadable, err).withLogs(logs)
	default:
		return exit().withLogs(logs)
	}
}

// exitOutcomeOf reads the failure out of how a process ended. A signal is what
// an OOM kill looks like, and Celery's own name for a child that disappeared
// is WorkerLostError: nothing of the body's own ran, so nothing released
// whatever it was holding.
func exitOutcomeOf(state *os.ProcessState, waitErr error) outcome {
	if state == nil {
		// The process is still there and said nothing this worker can use.
		return workerFailure(excUnreadable, errNoReport)
	}
	if number, name, killed := terminationSignal(state); killed {
		return workerFailure(excWorkerLost,
			fmt.Errorf("worker exited prematurely: signal %d (%s)", number, name))
	}
	if code := state.ExitCode(); code != 0 {
		return workerFailure(excWorkerLost,
			fmt.Errorf("worker exited prematurely: exitcode %d: %v", code, waitErr))
	}
	return workerFailure(excUnreadable, errNoReport)
}
