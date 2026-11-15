package notify

import (
	"encoding/json"
	"time"
)

// group is how an event collapses with others of its kind.
type group struct {
	// key names the group within the account ("source:failed").
	key string
	// window is how long after a row is first written later events may
	// still join it.
	window time.Duration
	// delay is how long the push of a new row waits, so a burst that is
	// still arriving is counted in it.
	delay time.Duration
	// item is this event's own name in the row's list ("Report.pdf").
	item string
	// render writes the row's title, message and push title for count
	// events, of which names are the latest few.
	render func(count int, names []string) rendered
}

// rendered is a collapsed row's words.
type rendered struct {
	title, message, pushTitle string
}

// keptNames is how many names a collapsed row lists: enough to recognise
// the batch, few enough for one line.
const keptNames = 5

// grow is the event rewritten to stand for itself and everything its group's
// row stood for already (previous is that row's metadata).
func (e event) grow(previous []byte) event {
	var before struct {
		Count int      `json:"count"`
		Names []string `json:"names"`
	}
	// A row this step wrote always reads; one that does not is counted as
	// one earlier event, which is what it was.
	if json.Unmarshal(previous, &before) != nil || before.Count < 1 {
		before.Count = 1
	}
	count := before.Count + 1
	names := append(append([]string(nil), before.Names...), e.group.item)
	if len(names) > keptNames {
		names = names[len(names)-keptNames:]
	}
	words := e.group.render(count, names)
	grown := e
	grown.title, grown.message, grown.push.Title = words.title, words.message, words.pushTitle
	grown.metadata = make(map[string]any, len(e.metadata)+2)
	for key, value := range e.metadata {
		grown.metadata[key] = value
	}
	grown.metadata["count"] = count
	grown.metadata["names"] = names
	return grown
}
