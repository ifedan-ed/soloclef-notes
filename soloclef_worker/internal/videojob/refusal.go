package videojob

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
)

// Refusal answers the message on a deployment with the render switched off
// (SOLOCLEF_WORKER_VIDEO_ENABLED=false): the row is marked failed at once,
// with a code the page's card has a sentence for, instead of spinning as
// "queued" until somebody looks.
type Refusal struct {
	Pool *pgxpool.Pool
	// Notifier tells the owner the video failed, as a render that failed
	// would.
	Notifier Notifier
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
	var owner string
	err = r.Pool.QueryRow(ctx, `
		UPDATE deliverable_jobs
		SET status = 'failed', phase = 'failed', failure_code = $2, internal_error = $3,
		    finished_at = now(), updated_at = now()
		WHERE id = $1 AND status IN ('queued', 'running')
		RETURNING user_id::text`, jobID, codeRender, refusedBecause).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		// Finished or cancelled already: nothing changed, nobody to tell.
		return nil
	}
	if err != nil {
		return fmt.Errorf("videojob: marking job %d failed: %w", jobID, err)
	}
	if err := r.Notifier.StudioFinished(ctx, notify.StudioRun{UserID: owner, JobID: jobID, Failed: true}); err != nil {
		return fmt.Errorf("videojob: job %d is marked failed and its notification could not be written: %w", jobID, err)
	}
	return nil
}
