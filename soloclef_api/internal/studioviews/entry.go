package studioviews

import "encoding/json"

// The reading view's state, kept in the report artifact's metadata under
// `report_view`, for the version (generation) of the report it was made
// for. A revised report is a new generation: it gets new suggestions, and
// the old ones, with their dismissals, are not carried over, because they
// were about sections that may no longer exist.
//
//	{"generation": 3, "language": "en", "ready": true,
//	 "suggestions": [{"id": "g3s1", "section": 0, "heading": "...",
//	                  "kind": "quiz", "title": "...", "focus": "...",
//	                  "state": "open" | "added" | "dismissed",
//	                  "output_id": null, "attempts": 0}],
//	 "feedback": null}
//
// `ready` is set once suggestions were written, even if there were none, so
// the model is asked once a version and not on every open. The shape is the
// Python tier's, so the entries it stored are read as they are.

// The three states of a suggestion.
const (
	stateOpen      = "open"
	stateAdded     = "added"
	stateDismissed = "dismissed"
)

// kept is one suggestion as stored.
type kept struct {
	ID string `json:"id"`
	// Section is the index of the section it sits under, from 0.
	Section int    `json:"section"`
	Heading string `json:"heading"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Focus   string `json:"focus"`
	State   string `json:"state"`
	// OutputID is the output an Add started.
	OutputID *int64 `json:"output_id"`
	// Attempts counts the Adds, so that adding again after a failed output
	// is a new request and a double click is not.
	Attempts int `json:"attempts"`
}

// entry is the stored state for one generation.
type entry struct {
	Generation  int64  `json:"generation"`
	Language    string `json:"language,omitempty"`
	Ready       bool   `json:"ready"`
	Suggestions []kept `json:"suggestions"`
	// Feedback is the retired Good/Bad rating. Nothing reads or sets it; a
	// stored one is written back exactly as it was found.
	Feedback json.RawMessage `json:"feedback"`
}

// entryOf is the stored entry for this generation, or nil when the metadata
// holds none, holds one for another generation, or holds something that is
// not an entry. A suggestion with no id, kind or title is left out: it
// cannot be shown or acted on, and must not fail the view for the others.
func entryOf(metadata []byte, generation int64) *entry {
	var fields map[string]json.RawMessage
	if json.Unmarshal(metadata, &fields) != nil {
		return nil
	}
	var head struct {
		Generation  *int64            `json:"generation"`
		Language    string            `json:"language"`
		Ready       bool              `json:"ready"`
		Suggestions []json.RawMessage `json:"suggestions"`
		Feedback    json.RawMessage   `json:"feedback"`
	}
	if json.Unmarshal(fields["report_view"], &head) != nil || head.Generation == nil || *head.Generation != generation {
		return nil
	}
	found := &entry{Generation: generation, Language: head.Language, Ready: head.Ready && head.Suggestions != nil,
		Suggestions: []kept{}, Feedback: head.Feedback}
	for _, raw := range head.Suggestions {
		var item kept
		if json.Unmarshal(raw, &item) != nil || item.ID == "" || item.Kind == "" || item.Title == "" {
			continue
		}
		if item.State != stateAdded && item.State != stateDismissed {
			item.State = stateOpen
		}
		found.Suggestions = append(found.Suggestions, item)
	}
	return found
}

// find is the suggestion with that id, or nil.
func (e *entry) find(id string) *kept {
	if e == nil {
		return nil
	}
	for i := range e.Suggestions {
		if e.Suggestions[i].ID == id {
			return &e.Suggestions[i]
		}
	}
	return nil
}

// encoded is the entry as stored. A nil rating is written as null, which is
// what it has always been.
func (e entry) encoded() ([]byte, error) {
	if len(e.Feedback) == 0 {
		e.Feedback = json.RawMessage(`null`)
	}
	if e.Suggestions == nil {
		e.Suggestions = []kept{}
	}
	return json.Marshal(e)
}
