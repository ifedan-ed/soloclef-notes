package studiogenerate

import (
	"encoding/json"
	"errors"
	"fmt"
)

// storedTaskVersion is the shape of an artifact job's request column that
// carries its task. Version 1 named only the output and its format: the task
// lived in the memory of the process that queued it, so a job outlived that
// process only as a failure. A version 1 row is never claimed (it has no
// task to run) and is closed by the sweep as interrupted.
const storedTaskVersion = 2

// storedTask is an artifact job's request column: everything a process needs
// to make the job, written once when the job is queued, so that any replica,
// and the process that starts after a deploy, can make it. The task is the
// whole prompt (the dialog's options and the sources' text, as read when the
// person asked), so every attempt makes the same thing from the same text;
// it is removed from the row once the job has ended (FinishJob).
type storedTask struct {
	Version int    `json:"version"`
	Output  string `json:"output"`
	Format  string `json:"format"`
	Task    string `json:"task"`
	// Style and Orientation are the infographic dialog's choices, the
	// answers to the one question a studio run is asked (Runner.answers);
	// empty for every other kind.
	Style       string `json:"style,omitempty"`
	Orientation string `json:"orientation,omitempty"`
}

// encodeTask is the request column a new artifact job is written with.
func encodeTask(output, format string, job Job) ([]byte, error) {
	if job.Task == "" {
		return nil, errors.New("studiogenerate: an artifact job needs a task")
	}
	return json.Marshal(storedTask{
		Version: storedTaskVersion, Output: output, Format: format,
		Task: job.Task, Style: job.Style, Orientation: job.Orientation,
	})
}

// decodeTask reads a claimed job's request column back into the job that is
// made. A row that does not hold a task of this version is refused rather
// than run with a guess: the claim only takes rows that hold a task, so this
// is a row written by something else.
func decodeTask(raw []byte, job *Job) error {
	var stored storedTask
	if err := json.Unmarshal(raw, &stored); err != nil {
		return fmt.Errorf("studiogenerate: the job's stored task is not JSON: %w", err)
	}
	if stored.Version != storedTaskVersion || stored.Task == "" {
		return fmt.Errorf("studiogenerate: the job's request is version %d with no task to run", stored.Version)
	}
	job.Task, job.Style, job.Orientation = stored.Task, stored.Style, stored.Orientation
	return nil
}
