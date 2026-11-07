package celery

import (
	"encoding/json"
	"fmt"
	"time"
)

// State is a Celery task state. Only the states this worker can produce are
// listed; PENDING is the absence of a result row, never written.
type State string

const (
	StateStarted State = "STARTED"
	StateSuccess State = "SUCCESS"
	StateFailure State = "FAILURE"
	StateRetry   State = "RETRY"
	StateRevoked State = "REVOKED"
)

// ResultKeyPrefix is the key the Python result backend reads. Anything that
// polls AsyncResult -- the chat and Studio progress endpoints among them --
// looks here, so the shape below is a contract with running clients, not an
// internal detail.
const ResultKeyPrefix = "celery-task-meta-"

// ResultKey names the backend key for a task id.
func ResultKey(taskID string) string { return ResultKeyPrefix + taskID }

// Result is one row of the result backend, in the extended shape this app
// configures (result_extended=True in app/celery_app.py adds name, args,
// kwargs, worker, retries and queue).
type Result struct {
	TaskID    string
	State     State
	Value     any
	Traceback string
	Name      string
	Args      []any
	Kwargs    map[string]any
	Worker    string
	Retries   int
	Queue     string
	DoneAt    time.Time
}

type resultJSON struct {
	Status   State          `json:"status"`
	Result   any            `json:"result"`
	Trace    *string        `json:"traceback"`
	Children []any          `json:"children"`
	DateDone *string        `json:"date_done"`
	Name     string         `json:"name"`
	Args     []any          `json:"args"`
	Kwargs   map[string]any `json:"kwargs"`
	Worker   string         `json:"worker"`
	Retries  int            `json:"retries"`
	Queue    string         `json:"queue"`
	TaskID   string         `json:"task_id"`
}

// ResultFor builds the row for a finished or starting task.
func ResultFor(msg *Message, worker string, state State) Result {
	return Result{
		TaskID:  msg.ID,
		State:   state,
		Name:    msg.Task,
		Args:    msg.Args,
		Kwargs:  msg.Kwargs,
		Worker:  worker,
		Retries: msg.Retries,
		Queue:   msg.Queue,
	}
}

// MarshalJSON writes the row exactly as the Python backend expects to read it:
// Python's json.loads gives None for a missing or null field, so an absent
// traceback and an unfinished date_done are null rather than "".
func (r Result) MarshalJSON() ([]byte, error) {
	out := resultJSON{
		Status:   r.State,
		Result:   r.Value,
		Children: []any{},
		Name:     r.Name,
		Args:     r.Args,
		Kwargs:   r.Kwargs,
		Worker:   r.Worker,
		Retries:  r.Retries,
		Queue:    r.Queue,
		TaskID:   r.TaskID,
	}
	if out.Args == nil {
		out.Args = []any{}
	}
	if out.Kwargs == nil {
		out.Kwargs = map[string]any{}
	}
	if r.Traceback != "" {
		trace := r.Traceback
		out.Trace = &trace
	}
	// STARTED is in flight, so it carries no completion time.
	if r.State != StateStarted && !r.DoneAt.IsZero() {
		done := r.DoneAt.UTC().Format("2006-01-02T15:04:05.000000-07:00")
		out.DateDone = &done
	}
	return json.Marshal(out)
}

// Terminal reports whether a state ends the task, and so whether its result may
// be allowed to expire.
func (s State) Terminal() bool {
	switch s {
	case StateSuccess, StateFailure, StateRevoked:
		return true
	default:
		return false
	}
}

// ErrorValue is the shape Python's result backend expects for a failure: the
// exception type and message, so AsyncResult.result reads like an exception
// rather than a bare string.
func ErrorValue(excType string, err error) map[string]any {
	message := ""
	if err != nil {
		message = err.Error()
	}
	if excType == "" {
		excType = "WorkerError"
	}
	return map[string]any{
		"exc_type":    excType,
		"exc_message": []any{message},
		"exc_module":  "builtins",
	}
}

func (r Result) String() string {
	return fmt.Sprintf("%s %s", r.TaskID, r.State)
}
