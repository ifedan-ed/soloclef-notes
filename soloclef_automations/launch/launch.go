// Package launch creates a run: the one statement of "this automation runs
// now", shared by every way a run starts (the Run now button in the API
// tier; a schedule or an event in the worker).
//
// A run is created only for an automation named with its owner, only while
// the automation is active, and only if its "if already running" setting
// allows another. The three are decided under the automation's row lock, so
// two triggers firing at once cannot both pass a check the other is about
// to fail.
package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"
)

// Outcome is how a launch ended.
type Outcome uint8

const (
	// Started means a pending run exists and its message should be
	// published.
	Started Outcome = iota + 1
	// NotFound means the account has no such automation.
	NotFound
	// NotActive means the automation is paused or archived.
	NotActive
	// AlreadyRunning means a run is waiting or running and the automation
	// drops a fire that arrives then.
	AlreadyRunning
)

// Request is one launch.
type Request struct {
	AutomationID int64
	// UserID is the automation's owner. It is required and is part of the
	// lookup: an automation is never started by its id alone.
	UserID string
	// TriggerID is the trigger that fired, or nil for a run started by
	// hand.
	TriggerID *int64
	// Inputs is what the trigger knows about this fire, as a JSON object.
	Inputs json.RawMessage
}

// Starter begins the transaction a launch runs in (*pgxpool.Pool).
type Starter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Run creates the run and returns its id when the outcome is Started.
func Run(ctx context.Context, db Starter, request Request) (int64, Outcome, error) {
	if request.UserID == "" {
		return 0, 0, errors.New("launch: a run needs its automation's owner")
	}
	inputs := request.Inputs
	if len(inputs) == 0 {
		inputs = json.RawMessage(`{}`)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("launch: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var status, concurrency string
	var snapshot []byte
	// The row lock is what makes the admission check below true for as long
	// as it takes to act on it. Every writer of an automation's runs takes
	// this lock first (the worker's claim does too), so the order is always
	// automation, then runs.
	err = tx.QueryRow(ctx, `
		SELECT status::text, definition, COALESCE(definition->'execution'->>'concurrency', '')
		FROM automations WHERE id = $1 AND user_id = $2::uuid FOR UPDATE`,
		request.AutomationID, request.UserID).Scan(&status, &snapshot, &concurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, NotFound, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("launch: reading the automation: %w", err)
	}
	if status != "active" {
		return 0, NotActive, nil
	}
	// Anything but the two settings that allow a second run drops the fire,
	// so a definition written before the setting existed is not given
	// overlapping runs nobody asked for.
	if concurrency != definition.Queue && concurrency != definition.Always {
		var busy bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM automation_runs
			               WHERE automation_id = $1 AND status IN ('pending', 'running'))`,
			request.AutomationID).Scan(&busy); err != nil {
			return 0, 0, fmt.Errorf("launch: reading the automation's runs: %w", err)
		}
		if busy {
			return 0, AlreadyRunning, nil
		}
	}
	var runID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO automation_runs (automation_id, trigger_id, status, definition_snapshot, inputs)
		VALUES ($1, $2, 'pending', $3, $4) RETURNING id`,
		request.AutomationID, request.TriggerID, snapshot, []byte(inputs)).Scan(&runID); err != nil {
		return 0, 0, fmt.Errorf("launch: creating the run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("launch: commit: %w", err)
	}
	return runID, Started, nil
}
