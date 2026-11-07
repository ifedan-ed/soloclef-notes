package executor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/celery"
)

// Report is what the task process writes on stdout, and the body's own word on
// what happened. The process keeps its stdout for this and nothing else: it
// points file descriptor 1 at its stderr before running a body, so a print or
// an ffmpeg's chatter lands in the logs instead of here.
type Report struct {
	Status string `json:"status"`

	// Result is the body's return value, encoded by kombu's JSON encoder, so
	// a UUID or a datetime reads back through AsyncResult exactly as it would
	// from a Celery worker.
	Result any `json:"result"`

	// The failure as Celery's result backend shapes one: the exception's type
	// and module, its args, and the formatted traceback.
	ExcType    string `json:"exc_type"`
	ExcModule  string `json:"exc_module"`
	ExcMessage []any  `json:"exc_message"`
	Traceback  string `json:"traceback"`
}

// Statuses the process may report. Anything else is a report this worker
// cannot act on, which is treated as no report at all.
const (
	reportSuccess = "SUCCESS"
	reportFailure = "FAILURE"
	reportRetry   = "RETRY"
)

var errNoReport = errors.New("executor: the task process said nothing about what happened")

// parseReport reads the last line of stdout. The last line rather than the
// whole stream because a library that writes to file descriptor 1 directly,
// before the process redirects it, would otherwise make an honest report
// unreadable.
func parseReport(stdout []byte) (*Report, error) {
	lines := bytes.Split(stdout, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var report Report
		if err := json.Unmarshal(line, &report); err != nil {
			return nil, fmt.Errorf("executor: the task process's report is not JSON: %w", err)
		}
		switch report.Status {
		case reportSuccess, reportFailure, reportRetry:
			return &report, nil
		default:
			return nil, fmt.Errorf("executor: the task process reported status %q", report.Status)
		}
	}
	return nil, errNoReport
}

// outcome turns the body's word into the row a client will read.
func (r *Report) outcome() outcome {
	switch r.Status {
	case reportSuccess:
		return outcome{state: celery.StateSuccess, value: r.Result}
	case reportRetry:
		// The body asked for a retry, and asking is publishing: Celery's own
		// Task.retry re-sends the message before raising, so the work is
		// already queued and this delivery is finished.
		return outcome{
			state:     celery.StateRetry,
			value:     r.errorValue(),
			traceback: r.Traceback,
			err:       fmt.Errorf("executor: the task asked to be retried: %s", r.failureText()),
		}
	default:
		return outcome{
			state:     celery.StateFailure,
			value:     r.errorValue(),
			traceback: r.Traceback,
			err:       fmt.Errorf("executor: the task failed: %s", r.failureText()),
		}
	}
}

// errorValue keeps the exception's own type and module rather than restating
// it as a worker error: a failure that came from the body should read, through
// AsyncResult, as the exception the body raised.
func (r *Report) errorValue() map[string]any {
	excType := r.ExcType
	if excType == "" {
		excType = "TaskError"
	}
	excModule := r.ExcModule
	if excModule == "" {
		excModule = "builtins"
	}
	message := r.ExcMessage
	if message == nil {
		message = []any{}
	}
	return map[string]any{
		"exc_type":    excType,
		"exc_message": message,
		"exc_module":  excModule,
	}
}

func (r *Report) failureText() string {
	if len(r.ExcMessage) > 0 {
		return fmt.Sprintf("%s: %v", r.ExcType, r.ExcMessage[0])
	}
	return r.ExcType
}
