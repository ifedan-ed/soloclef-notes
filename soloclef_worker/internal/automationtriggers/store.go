package automationtriggers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"
)

// Store is Rows in Postgres.
type Store struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger
}

func (s Store) Begin(ctx context.Context) (pgx.Tx, error) { return s.Pool.Begin(ctx) }

// ClaimDue advances the due schedule triggers and returns the fires.
//
// The rows are locked with SKIP LOCKED, so two ticks that overlap split the
// due triggers between them and neither waits. A trigger with no next fire
// (written some other way) is given one and does not fire. A trigger whose
// schedule can no longer be read is switched off, and said in the log,
// rather than left to fail on every tick. A trigger of a paused automation
// is advanced without firing, so that resuming does not fire every moment
// that passed while it was paused.
func (s Store) ClaimDue(ctx context.Context, now time.Time, limit int) ([]Fire, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT t.id, t.automation_id, a.user_id::text, a.status::text, t.params, t.static_inputs, t.next_fire_at, t.last_fired_at
		FROM automation_triggers AS t
		JOIN automations AS a ON a.id = t.automation_id
		WHERE t.type = 'schedule' AND t.enabled AND (t.next_fire_at IS NULL OR t.next_fire_at <= $1)
		ORDER BY t.next_fire_at NULLS FIRST, t.id
		LIMIT $2
		FOR UPDATE OF t SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	type due struct {
		id, automationID int64
		userID, status   string
		params, static   []byte
		scheduled, last  *time.Time
	}
	claimed, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (due, error) {
		var d due
		err := row.Scan(&d.id, &d.automationID, &d.userID, &d.status, &d.params, &d.static, &d.scheduled, &d.last)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	var fires []Fire
	for _, d := range claimed {
		trigger, err := definition.ParseTrigger(definition.TriggerSchedule, d.params)
		if err != nil {
			s.Log.Warn("a schedule trigger cannot be read and was switched off", "trigger", d.id, "error", err.Error())
			if _, err := tx.Exec(ctx, `UPDATE automation_triggers SET enabled = false, next_fire_at = NULL WHERE id = $1`, d.id); err != nil {
				return nil, err
			}
			continue
		}
		next := trigger.NextFire(true, now)
		firesNow := d.scheduled != nil && d.status == "active"
		if _, err := tx.Exec(ctx, `
			UPDATE automation_triggers
			SET next_fire_at = $2, last_fired_at = CASE WHEN $3 THEN $4 ELSE last_fired_at END
			WHERE id = $1`, d.id, next, firesNow, now); err != nil {
			return nil, err
		}
		if !firesNow {
			continue
		}
		inputs, err := scheduleInputs(d.static, now, *d.scheduled, d.last)
		if err != nil {
			return nil, fmt.Errorf("trigger %d: %w", d.id, err)
		}
		fires = append(fires, Fire{TriggerID: d.id, AutomationID: d.automationID, UserID: d.userID, Inputs: inputs})
	}
	return fires, tx.Commit(ctx)
}

// merged is a trigger's own fixed inputs with what this fire adds; the
// fire's facts win, so a static input cannot pose as the time or the event.
func merged(static []byte, fire map[string]any) (json.RawMessage, error) {
	inputs := map[string]any{}
	if len(static) > 0 {
		if err := json.Unmarshal(static, &inputs); err != nil {
			return nil, fmt.Errorf("the trigger's static inputs are not an object: %w", err)
		}
	}
	for key, value := range fire {
		inputs[key] = value
	}
	return json.Marshal(inputs)
}

// scheduleInputs is what a scheduled run is told: when it fired, the moment
// it was scheduled for, and the fire before it, so a task can say "since
// the last run".
func scheduleInputs(static []byte, firedAt, scheduledFor time.Time, last *time.Time) (json.RawMessage, error) {
	fire := map[string]any{
		"fired_at":      firedAt.UTC().Format(time.RFC3339),
		"scheduled_for": scheduledFor.UTC().Format(time.RFC3339),
		"last_fired_at": nil,
	}
	if last != nil {
		fire["last_fired_at"] = last.UTC().Format(time.RFC3339)
	}
	return merged(static, fire)
}

func (s Store) StalePending(ctx context.Context, waited time.Duration, limit int) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id FROM automation_runs
		WHERE status = 'pending' AND created_at < now() - make_interval(secs => $1)
		ORDER BY id LIMIT $2`, waited.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// TimeOutOverdue ends runs whose worker is gone. The limit is the run's own
// (its definition snapshot's), read defensively: a snapshot with no usable
// number is given the default, and none is trusted past a day.
func (s Store) TimeOutOverdue(ctx context.Context, grace time.Duration) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE automation_runs
		SET status = 'timed_out', finished_at = now(),
		    error = '{"type":"timed_out","message":"The run passed its time limit and its worker did not end it; it was stopped."}'::jsonb
		WHERE status = 'running'
		  AND started_at + make_interval(secs => LEAST(
		        CASE WHEN definition_snapshot->'execution'->>'timeout_seconds' ~ '^[0-9]{1,9}$'
		             THEN (definition_snapshot->'execution'->>'timeout_seconds')::int ELSE 600 END,
		        $2)) + make_interval(secs => $1) < now()`, grace.Seconds(), definition.MaxTimeoutSeconds)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s Store) Fired(ctx context.Context, triggerID int64, at time.Time) error {
	_, err := s.Pool.Exec(ctx, `UPDATE automation_triggers SET last_fired_at = $2 WHERE id = $1`, triggerID, at)
	return err
}

func (s Store) AlreadyFired(ctx context.Context, triggerID int64, eventID string) (bool, error) {
	var fired bool
	err := s.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM automation_runs WHERE trigger_id = $1 AND inputs->>'event_id' = $2)`,
		triggerID, eventID).Scan(&fired)
	return fired, err
}

// EventTrigger is one event trigger as the reader matches it.
type EventTrigger struct {
	ID           int64
	AutomationID int64
	Params       []byte
	StaticInputs []byte
}

// EventTriggers reads the triggers an event may fire: the event's own
// account's, and no other's. The account is in the statement, so an event
// can never reach another account's automation whatever its filter says.
func (s Store) EventTriggers(ctx context.Context, userID, eventType string) ([]EventTrigger, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT t.id, t.automation_id, t.params, t.static_inputs
		FROM automation_triggers AS t
		JOIN automations AS a ON a.id = t.automation_id
		WHERE a.user_id = $1::uuid AND a.status = 'active'
		  AND t.type = 'event' AND t.enabled AND t.params->>'event_type' = $2
		ORDER BY t.id`, userID, eventType)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (EventTrigger, error) {
		var t EventTrigger
		err := row.Scan(&t.ID, &t.AutomationID, &t.Params, &t.StaticInputs)
		return t, err
	})
}
