package notebookname

import (
	"encoding/json"
	"time"
)

// When a notebook may name itself, and from what (app/notebooks/autoname/
// policy.py):
//
//   - only while `title_source` is "auto": the first rename by the person
//     sets it to "user", and naming stops for good;
//   - the first name comes from the sources once one is ready, or, with none
//     ready yet, from the first question;
//   - one refinement: when more sources are ready than the last name used,
//     within refineWindow of it, the notebook is named again from all of
//     them (a batch upload whose files finish one by one);
//   - never more than maxRuns names in all.
const (
	maxRuns      = 2
	refineWindow = 30 * time.Minute
)

// Basis is what a name is drawn from.
type Basis string

const (
	fromSources  Basis = "sources"
	fromQuestion Basis = "question"
)

// Trigger is what queued the job.
type Trigger string

const (
	triggerSource   Trigger = "source"
	triggerQuestion Trigger = "question"
)

// state is the notebook's `auto_title` column, read leniently: anything
// that is not the expected object counts as never named.
type state struct {
	titleSource string
	runs        int
	at          time.Time
	// sources is how many ready sources the last name used; 0 means it came
	// from a question.
	sources int
}

func readState(titleSource string, column []byte) state {
	read := state{titleSource: titleSource}
	if read.titleSource == "" {
		read.titleSource = "user"
	}
	var stored struct {
		Runs    any    `json:"runs"`
		At      string `json:"at"`
		Sources any    `json:"sources"`
	}
	if len(column) == 0 || json.Unmarshal(column, &stored) != nil {
		return read
	}
	read.runs = whole(stored.Runs)
	read.sources = whole(stored.Sources)
	if at, err := time.Parse(time.RFC3339Nano, stored.At); err == nil {
		read.at = at
	}
	return read
}

func whole(value any) int {
	number, isNumber := value.(float64)
	if !isNumber || number < 0 || number != float64(int(number)) {
		return 0
	}
	return int(number)
}

// decide is what to name the notebook from now, or "" to leave it alone.
func decide(current state, readySources int, hasQuestion bool, trigger Trigger, now time.Time) Basis {
	if current.titleSource != "auto" || current.runs >= maxRuns {
		return ""
	}
	if current.runs == 0 {
		switch {
		case readySources > 0:
			return fromSources
		case hasQuestion:
			return fromQuestion
		}
		return ""
	}
	// Named once already: only newly ready sources refine it, and only soon
	// after.
	if trigger != triggerSource || readySources <= current.sources {
		return ""
	}
	if current.at.IsZero() || now.Sub(current.at) > refineWindow {
		return ""
	}
	return fromSources
}

// next is the column after a naming.
func next(runs int, basis Basis, readySources int, now time.Time) []byte {
	sources := 0
	if basis == fromSources {
		sources = readySources
	}
	encoded, _ := json.Marshal(map[string]any{
		"runs": runs + 1, "at": now.UTC().Format(time.RFC3339Nano), "sources": sources, "basis": string(basis),
	})
	return encoded
}
