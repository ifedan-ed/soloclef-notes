// Package celery reads and writes Celery's protocol so this worker can consume
// the queues the FastAPI app already produces to, unchanged. The API keeps
// enqueuing through Celery's Python client; only the consumer moves here, which
// is what makes the move reversible one queue at a time.
package celery

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotATask is returned for an envelope that is syntactically fine but is not
// a protocol 2 task message, so a caller can drop it instead of retrying it.
var ErrNotATask = errors.New("celery: not a protocol 2 task message")

// Message is one task as the broker holds it.
type Message struct {
	Task     string
	ID       string
	RootID   string
	ParentID string
	Retries  int

	// Queue the message was routed to, from properties.delivery_info.
	Queue string

	// Exchange the message was published to, from properties.delivery_info.
	// Celery publishes a routed task through the anonymous exchange even
	// though each queue has a direct exchange of its own, so this is empty
	// for everything this app sends. It is read because Celery's own
	// ack-emulation registry stores it alongside the payload.
	Exchange string

	// DeliveryTag is the per-delivery id the producer stamped into
	// properties.delivery_tag. On the Redis transport there is no broker ack,
	// so this tag is the key of the unacked registry that stands in for one.
	DeliveryTag string

	// Priority picks which of a queue's priority lists holds the message.
	// Every Soloclef task publishes at 0 today.
	Priority int

	// Redelivered is set by whichever worker restored this message after a
	// visibility window elapsed, so a second delivery is never silent.
	Redelivered bool

	// Zero when the producer set none.
	ETA     time.Time
	Expires time.Time

	// From headers.timelimit [soft, hard]; zero when unset.
	SoftTimeLimit time.Duration
	HardTimeLimit time.Duration

	Args   []any
	Kwargs map[string]any

	// EnqueuedAt comes from this app's own "surfsense.enqueued_at_ns" header
	// (app/celery_app.py), and is the only way to measure true queue wait:
	// the broker records nothing itself. Zero when the header is absent.
	EnqueuedAt time.Time

	// Raw is the envelope exactly as read, for requeueing a message this
	// worker will not run without re-encoding — and so losing a field we do
	// not model is never silent.
	Raw []byte
}

type envelope struct {
	Body            string         `json:"body"`
	ContentEncoding string         `json:"content-encoding"`
	ContentType     string         `json:"content-type"`
	Headers         messageHeaders `json:"headers"`
	Properties      msgProperties  `json:"properties"`
}

type messageHeaders struct {
	Lang         string  `json:"lang"`
	Task         string  `json:"task"`
	ID           string  `json:"id"`
	RootID       string  `json:"root_id"`
	ParentID     string  `json:"parent_id"`
	Retries      int     `json:"retries"`
	ETA          *string `json:"eta"`
	Expires      *string `json:"expires"`
	TimeLimit    []*int  `json:"timelimit"`
	EnqueuedAtNs string  `json:"surfsense.enqueued_at_ns"`
	Redelivered  bool    `json:"redelivered"`
}

type msgProperties struct {
	BodyEncoding string `json:"body_encoding"`
	DeliveryTag  string `json:"delivery_tag"`
	Priority     int    `json:"priority"`
	DeliveryInfo struct {
		Exchange   string `json:"exchange"`
		RoutingKey string `json:"routing_key"`
	} `json:"delivery_info"`
}

// Decode reads one envelope as taken off the broker.
func Decode(raw []byte) (*Message, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("celery: envelope is not JSON: %w", err)
	}
	if env.Headers.Task == "" {
		return nil, ErrNotATask
	}
	if env.ContentType != "" && env.ContentType != "application/json" {
		return nil, fmt.Errorf("celery: unsupported content-type %q", env.ContentType)
	}

	body := []byte(env.Body)
	switch env.Properties.BodyEncoding {
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(env.Body)
		if err != nil {
			return nil, fmt.Errorf("celery: body is not base64: %w", err)
		}
		body = decoded
	case "", "utf-8":
		// Body is the JSON itself.
	default:
		return nil, fmt.Errorf("celery: unsupported body_encoding %q", env.Properties.BodyEncoding)
	}

	// Protocol 2 body: [args, kwargs, embed].
	var parts []json.RawMessage
	if err := json.Unmarshal(body, &parts); err != nil {
		return nil, fmt.Errorf("celery: body is not a JSON array: %w", err)
	}
	if len(parts) != 3 {
		return nil, fmt.Errorf("celery: body has %d parts, want 3", len(parts))
	}

	msg := &Message{
		Task:     env.Headers.Task,
		ID:       env.Headers.ID,
		RootID:   env.Headers.RootID,
		ParentID: env.Headers.ParentID,
		Retries:  env.Headers.Retries,
		Queue:    env.Properties.DeliveryInfo.RoutingKey,
		Kwargs:   map[string]any{},
		Raw:      raw,

		Exchange:    env.Properties.DeliveryInfo.Exchange,
		DeliveryTag: env.Properties.DeliveryTag,
		Priority:    env.Properties.Priority,
		Redelivered: env.Headers.Redelivered,
	}
	if err := json.Unmarshal(parts[0], &msg.Args); err != nil {
		return nil, fmt.Errorf("celery: args: %w", err)
	}
	if err := json.Unmarshal(parts[1], &msg.Kwargs); err != nil {
		return nil, fmt.Errorf("celery: kwargs: %w", err)
	}

	var err error
	if msg.ETA, err = parseOptionalTime(env.Headers.ETA); err != nil {
		return nil, fmt.Errorf("celery: eta: %w", err)
	}
	if msg.Expires, err = parseOptionalTime(env.Headers.Expires); err != nil {
		return nil, fmt.Errorf("celery: expires: %w", err)
	}
	msg.SoftTimeLimit, msg.HardTimeLimit = parseTimeLimit(env.Headers.TimeLimit)
	msg.EnqueuedAt = parseEnqueuedAt(env.Headers.EnqueuedAtNs)

	return msg, nil
}

// Due reports whether the message may run now: an eta in the future means hold
// it, and anything past its expiry must be discarded rather than run late.
func (m *Message) Due(now time.Time) (due bool, expired bool) {
	if !m.Expires.IsZero() && now.After(m.Expires) {
		return false, true
	}
	if !m.ETA.IsZero() && now.Before(m.ETA) {
		return false, false
	}
	return true, false
}

func parseOptionalTime(value *string) (time.Time, error) {
	if value == nil || *value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func parseTimeLimit(limits []*int) (soft, hard time.Duration) {
	if len(limits) > 0 && limits[0] != nil {
		soft = time.Duration(*limits[0]) * time.Second
	}
	if len(limits) > 1 && limits[1] != nil {
		hard = time.Duration(*limits[1]) * time.Second
	}
	return soft, hard
}

func parseEnqueuedAt(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	var ns int64
	if _, err := fmt.Sscanf(value, "%d", &ns); err != nil || ns <= 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}
