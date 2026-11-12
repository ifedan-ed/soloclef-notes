package automationtriggers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"
	"git.danvics.com/danvics/soloclef/soloclef_automations/eventfilter"
	"git.danvics.com/danvics/soloclef/soloclef_automations/eventwire"
)

// Events reads the domain-event stream and fires the event triggers each
// event matches.
//
// The stream is read through its consumer group, so every worker may read
// and each event is handed to one of them. An entry is acknowledged after
// its triggers were handled; one whose reader died first stays pending and
// is taken over by another reader once it has sat for ClaimIdle. Because
// that can hand an event to a second reader after the first started its
// runs, a trigger is fired for an event at most once: the run records the
// event's id, and a trigger that already has a run for it is skipped.
//
// An event fires only its own account's triggers (Store.EventTriggers).
type Events struct {
	// Redis is the stream's server: the API tier's Redis, which is not
	// necessarily the broker's.
	Redis   redis.UniversalClient
	Starter Starter
	// Consumer names this reader within the group.
	Consumer string
	// Block is how long one read waits for an entry; ClaimIdle how long an
	// entry may sit unacknowledged before another reader takes it; Batch
	// how many entries one read takes.
	Block     time.Duration
	ClaimIdle time.Duration
	Batch     int64
	Now       func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (e *Events) Check() error {
	switch {
	case e.Redis == nil || e.Starter.Rows == nil || e.Starter.Publisher == nil || e.Starter.Log == nil || e.Now == nil:
		return errors.New("automationtriggers: an event reader's dependency is missing")
	case e.Consumer == "" || e.Block <= 0 || e.ClaimIdle <= 0 || e.Batch < 1:
		return errors.New("automationtriggers: the event reader's settings are incomplete")
	}
	return nil
}

// Run reads until ctx ends. A read that fails is retried after a pause: a
// Redis that is away must not end the reader for good.
func (e *Events) Run(ctx context.Context) {
	log := e.Starter.Log
	for ctx.Err() == nil {
		if err := e.ensureGroup(ctx); err == nil {
			err = e.Once(ctx)
			if err == nil || ctx.Err() != nil {
				continue
			}
			log.Warn("the event stream could not be read; retrying", "error", err.Error())
		} else if ctx.Err() == nil {
			log.Warn("the event stream's group could not be made; retrying", "error", err.Error())
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

// ensureGroup makes the consumer group, and the stream with it, if either
// is not there. A group that exists is the usual case and is not an error.
func (e *Events) ensureGroup(ctx context.Context) error {
	// "$": a group made now reads what is published from now on. Events
	// from before any reader existed describe arrivals nobody was waiting
	// for.
	err := e.Redis.XGroupCreateMkStream(ctx, eventwire.Stream, eventwire.Group, "$").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// Once takes over entries another reader abandoned, then reads new ones,
// handling and acknowledging each.
func (e *Events) Once(ctx context.Context) error {
	abandoned, _, err := e.Redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: eventwire.Stream, Group: eventwire.Group, Consumer: e.Consumer,
		MinIdle: e.ClaimIdle, Start: "0", Count: e.Batch,
	}).Result()
	if err != nil {
		return fmt.Errorf("taking over abandoned events: %w", err)
	}
	e.handle(ctx, abandoned)
	streams, err := e.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: eventwire.Group, Consumer: e.Consumer, Streams: []string{eventwire.Stream, ">"},
		Count: e.Batch, Block: e.Block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		// Nothing arrived within Block.
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading events: %w", err)
	}
	for _, stream := range streams {
		e.handle(ctx, stream.Messages)
	}
	return nil
}

func (e *Events) handle(ctx context.Context, entries []redis.XMessage) {
	log := e.Starter.Log
	for _, entry := range entries {
		field, _ := entry.Values[eventwire.Field].(string)
		event, err := eventwire.Decode(field)
		if err != nil {
			// Not an event this reader can act on, now or later: said and
			// acknowledged, so it is not read again for ever.
			log.Warn("an entry of the event stream was skipped", "entry", entry.ID, "error", err.Error())
		} else if err := e.Fire(ctx, event); err != nil {
			// Left unacknowledged: it is taken over and tried again, and
			// the at-most-once check keeps the triggers that did fire from
			// firing twice.
			log.Warn("an event's triggers could not all be fired; it will be read again", "event", event.EventID, "error", err.Error())
			continue
		}
		if err := e.Redis.XAck(ctx, eventwire.Stream, eventwire.Group, entry.ID).Err(); err != nil {
			log.Warn("an event was not acknowledged; it will be read again", "entry", entry.ID, "error", err.Error())
		}
	}
}

// Fire starts a run for each of the account's triggers the event matches.
func (e *Events) Fire(ctx context.Context, event eventwire.Event) error {
	triggers, err := e.Starter.Rows.EventTriggers(ctx, event.UserID, event.EventType)
	if err != nil {
		return fmt.Errorf("reading the account's event triggers: %w", err)
	}
	if len(triggers) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		// A payload that is not an object matches no filter and never will.
		e.Starter.Log.Warn("an event's payload is not an object; no trigger can match it", "event", event.EventID)
		return nil
	}
	var failures []error
	for _, trigger := range triggers {
		if err := e.fireOne(ctx, trigger, event, payload); err != nil {
			failures = append(failures, fmt.Errorf("trigger %d: %w", trigger.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (e *Events) fireOne(ctx context.Context, trigger EventTrigger, event eventwire.Event, payload map[string]any) error {
	parsed, err := definition.ParseTrigger(definition.TriggerEvent, trigger.Params)
	if err != nil {
		// Saved before a rule existed. It cannot match; it is said and
		// left for its owner to fix, and does not hold the event back.
		e.Starter.Log.Warn("an event trigger cannot be read and was skipped", "trigger", trigger.ID, "error", err.Error())
		return nil
	}
	matched, err := eventfilter.Matches(parsed.Event.Filter, payload)
	if err != nil || !matched {
		return err
	}
	fired, err := e.Starter.Rows.AlreadyFired(ctx, trigger.ID, event.EventID)
	if err != nil || fired {
		return err
	}
	// The run is told the event: its payload, flattened, and which event it
	// was. The event's own facts win over a static input of the same name.
	fire := map[string]any{}
	for key, value := range payload {
		fire[key] = value
	}
	fire["event_type"], fire["event_id"], fire["occurred_at"] = event.EventType, event.EventID, event.OccurredAt
	inputs, err := merged(trigger.StaticInputs, fire)
	if err != nil {
		return err
	}
	started, err := e.Starter.Start(ctx, Fire{TriggerID: trigger.ID, AutomationID: trigger.AutomationID, UserID: event.UserID, Inputs: inputs})
	if err != nil || !started {
		return err
	}
	return e.Starter.Rows.Fired(ctx, trigger.ID, e.Now())
}
