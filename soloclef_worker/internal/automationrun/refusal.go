package automationrun

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
)

// SwitchedOff is the job on a deployment that switched automations off: every
// run that reaches it is ended at once with a sentence saying so, instead
// of waiting for ever for a worker that will never run it.
type SwitchedOff struct {
	Pool *pgxpool.Pool
}

func (r SwitchedOff) Run(ctx context.Context, msg *celery.Message) error {
	runID, err := runIDOf(msg)
	if err != nil {
		return err
	}
	_, err = r.Pool.Exec(ctx, `
		UPDATE automation_runs
		SET status = 'failed', finished_at = now(),
		    error = '{"type":"disabled","message":"Automations are switched off on this server, so this run was not started."}'::jsonb
		WHERE id = $1 AND status = 'pending'`, runID)
	if err != nil {
		return fmt.Errorf("automationrun: refusing run %d: %w", runID, err)
	}
	return nil
}
