package videojob

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
)

// Refusal answers the message on a deployment with the render switched off
// (SOLOCLEF_WORKER_VIDEO_ENABLED=false): the row is marked failed at once,
// with a code the page's card has a sentence for, instead of spinning as
// "queued" until somebody looks.
type Refusal struct {
	Pool *pgxpool.Pool
}

// refusedBecause is the row's internal_error: why this job did not run.
const refusedBecause = "video rendering is switched off on this server (SOLOCLEF_WORKER_VIDEO_ENABLED=false)"

// Run is the jobs.Handler. A job already finished or cancelled is left as
// it is.
func (r Refusal) Run(ctx context.Context, msg *celery.Message) error {
	jobID, err := jobOf(msg)
	if err != nil {
		return err
	}
	_, err = r.Pool.Exec(ctx, `
		UPDATE deliverable_jobs
		SET status = 'failed', phase = 'failed', failure_code = $2, internal_error = $3,
		    finished_at = now(), updated_at = now()
		WHERE id = $1 AND status IN ('queued', 'running')`, jobID, codeRender, refusedBecause)
	if err != nil {
		return fmt.Errorf("videojob: marking job %d failed: %w", jobID, err)
	}
	return nil
}
