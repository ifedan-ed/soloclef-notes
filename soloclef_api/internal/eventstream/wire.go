// Package eventstream publishes a Soloclef domain event: one entry on a Redis
// Stream that whatever reacts to events (automations, today) reads through a
// consumer group.
//
// An event belongs to an account. `user_id` on the entry is the owner of the
// thing the event is about, and a subscriber acts only for that account, so
// one account's event can never start another account's automation.
//
// This package is self-contained: nothing else in soloclef_api imports it but
// cmd/api, which adapts it to the seam of whichever route owes an event.
package eventstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"git.danvics.com/danvics/soloclef/soloclef_automations/eventwire"
	"time"
)

// Stream, Group and Field name the stream, its consumer group and the one
// field of an entry. Publisher and subscribers must agree on all three.
//
// They are the worker's too, which reads the stream for event triggers, so
// they are written once, in the module both tiers require.
const (
	Stream = eventwire.Stream
	Group  = eventwire.Group
	Field  = eventwire.Field
)

// RetentionEntries caps the stream's history; each publish trims to it.
const RetentionEntries = 10000

// Event is one entry as a subscriber reads it back.
type Event struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	// UserID is the account the event belongs to, as the uuid's text.
	UserID     string `json:"user_id"`
	EventID    string `json:"event_id"`
	OccurredAt string `json:"occurred_at"`
}

// ErrNoOwner refuses an event that names no account. An entry without an
// owner could be acted on for nobody or, worse, for everybody, so it is never
// written.
var ErrNoOwner = errors.New("eventstream: an event must name the account it belongs to")

// NewEvent stamps an event for one account with its identity and its time.
func NewEvent(eventType string, payload any, userID string, at time.Time) (Event, error) {
	if userID == "" {
		return Event{}, ErrNoOwner
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encoding the %s payload: %w", eventType, err)
	}
	id, err := newEventID()
	if err != nil {
		return Event{}, err
	}
	return Event{
		EventType:  eventType,
		Payload:    encoded,
		UserID:     userID,
		EventID:    id,
		OccurredAt: eventTime(at),
	}, nil
}

// eventTime writes an instant as RFC 3339 in UTC with `Z`: six fractional
// digits, and no fraction at all when there are no microseconds to show.
func eventTime(at time.Time) string {
	utc := at.UTC()
	text := utc.Format("2006-01-02T15:04:05")
	if microseconds := utc.Nanosecond() / 1000; microseconds != 0 {
		text += fmt.Sprintf(".%06d", microseconds)
	}
	return text + "Z"
}

// Encode is the field map for one entry.
func (e Event) Encode() (map[string]string, error) {
	body, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("encoding the %s event: %w", e.EventType, err)
	}
	return map[string]string{Field: string(body)}, nil
}
