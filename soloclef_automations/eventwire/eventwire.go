// Package eventwire is the Redis Stream domain events travel on, as its
// publisher (the API tier's eventstream) and its reader (the worker's
// event triggers) must both know it: the stream's key, the consumer group,
// the one field of an entry and the JSON that field holds.
package eventwire

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Stream, Group and Field name the stream, its consumer group and the one
// field of an entry.
const (
	Stream = "soloclef:events"
	Group  = "soloclef:events:dispatch"
	Field  = "event"
)

// Event is one entry as it is read back.
type Event struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	// UserID is the account the event belongs to. A reader acts only for
	// that account, so one account's event never starts another's work.
	UserID     string `json:"user_id"`
	EventID    string `json:"event_id"`
	OccurredAt string `json:"occurred_at"`
}

// Decode reads an entry's field. An entry that names no account, no type or
// no id is refused: it could be acted on for nobody, or for everybody.
func Decode(field string) (Event, error) {
	var event Event
	if err := json.Unmarshal([]byte(field), &event); err != nil {
		return Event{}, fmt.Errorf("eventwire: the entry is not an event: %w", err)
	}
	if event.UserID == "" || event.EventType == "" || event.EventID == "" {
		return Event{}, errors.New("eventwire: the entry names no account, no type or no id")
	}
	return event, nil
}
