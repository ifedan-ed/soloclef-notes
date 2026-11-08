// Package eventstream publishes a Soloclef domain event from this tier.
//
// The event bus's subscribers are Python and stay Python; what crosses here is
// the publish, over the Redis Stream app/event_bus/stream/wire.py names. This
// package is self-contained — nothing else in soloclef_api imports it — so a
// route that owes an event asks for it and nothing else changes.
package eventstream

import (
	"encoding/json"
	"fmt"
	"time"
)

// Stream, Group and Field are app/event_bus/stream/wire.py's three constants.
const (
	Stream = "soloclef:events"
	Group  = "soloclef:events:dispatch"
	Field  = "event"
)

// RetentionEntries is that module's cap, so a publish from here trims the
// history the same way a publish from there does.
const RetentionEntries = 10000

// Event is app.event_bus.event.Event. The field order is the pydantic model's,
// because the entry a subscriber reads back should be the same bytes whichever
// tier wrote it.
type Event struct {
	EventType   string          `json:"event_type"`
	Payload     json.RawMessage `json:"payload"`
	WorkspaceID int64           `json:"workspace_id"`
	EventID     string          `json:"event_id"`
	OccurredAt  string          `json:"occurred_at"`
}

// NewEvent stamps the identity and the time the engine stamps there.
func NewEvent(eventType string, payload any, workspaceID int64, at time.Time) (Event, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encoding the %s payload: %w", eventType, err)
	}
	id, err := newEventID()
	if err != nil {
		return Event{}, err
	}
	return Event{
		EventType:   eventType,
		Payload:     encoded,
		WorkspaceID: workspaceID,
		EventID:     id,
		OccurredAt:  pydanticTime(at),
	}, nil
}

// pydanticTime is how pydantic's JSON serializer writes an aware UTC datetime:
// RFC 3339 with `Z`, six fractional digits, and no fraction at all when there
// are no microseconds to show.
func pydanticTime(at time.Time) string {
	utc := at.UTC()
	text := utc.Format("2006-01-02T15:04:05")
	if microseconds := utc.Nanosecond() / 1000; microseconds != 0 {
		text += fmt.Sprintf(".%06d", microseconds)
	}
	return text + "Z"
}

// Encode is wire.encode: the field map for one entry.
func (e Event) Encode() (map[string]string, error) {
	body, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("encoding the %s event: %w", e.EventType, err)
	}
	return map[string]string{Field: string(body)}, nil
}
