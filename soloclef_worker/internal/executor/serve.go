package executor

import "encoding/json"

// The warm protocol, in one place: what a long-lived task process is asked for
// and what it says back (soloclef_backend/app/tasks/serve.py). The report
// inside it is `app/tasks/run.py`'s report, unchanged, because a row must not
// depend on which shape ran the body.
//
// One request line in, one report line out, newline-delimited JSON both ways.
// The process keeps stdout for these lines and nothing else: it points file
// descriptor 1 at its stderr before importing anything, so a print or an
// ffmpeg's chatter lands in the worker's logs.

// request is one job as the warm process reads it. The task name is in the
// line rather than on argv, because a warm process runs whatever comes next.
type request struct {
	Task string `json:"task"`
	Call Call   `json:"call"`
}

// response is a line the warm process writes: either that it is ready for
// work, or a report for the job it names.
type response struct {
	// Ready is written once, after the imports, and is what tells this worker
	// the process is a process it can give work to.
	Ready bool `json:"ready"`

	// PID is the process's own, for the logs: it is the group this worker
	// signals.
	PID int `json:"pid"`

	// ID is the task id the report belongs to. A report for any other job is
	// a process out of step with its work, which is retired rather than
	// trusted.
	ID string `json:"id"`

	// Report is run.py's report verbatim, read by the same parser the one-shot
	// shape uses.
	Report json.RawMessage `json:"report"`
}

func encodeRequest(task string, call Call) ([]byte, error) {
	line, err := json.Marshal(request{Task: task, Call: call})
	if err != nil {
		return nil, err
	}
	return append(line, '\n'), nil
}
