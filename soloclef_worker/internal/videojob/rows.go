package videojob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_studio/artifactsave"
)

// The job's lifecycle is the deliverable_jobs state machine the API tier
// and the page read: queued, running, cancelling, then ready, failed or
// cancelled. The API tier's chat writes a row queued; its cancel route
// moves a queued row to cancelled and a running one to cancelling; its retry
// route moves a failed or cancelled row back to queued under the next
// attempt's task id. Everything else is this file's.
//
// Every statement after the claim names the owner and the attempt's task id
// beside the job's id, and changes the row only from the state it expects:
// an attempt that was cancelled, retried or taken over finds its writes
// refused, and stops.

// Claimed is one attempt of one job, as this worker holds it.
type Claimed struct {
	ID         int64
	UserID     string
	Kind       string
	Title      string
	ToolCallID string
	// ThreadID is the conversation, or nil when it was deleted.
	ThreadID *int64
	// TaskID is the attempt's message id ("deliverable-job:7:attempt:2").
	TaskID  string
	Attempt int
	Request []byte
	// Takeovers is how many times this attempt has been taken over from a
	// worker that stopped answering.
	Takeovers int
}

// sandboxName is the attempt's sandbox: one per attempt, so a retry starts
// in a clean one and a takeover finds the dead worker's to remove.
func (c Claimed) sandboxName() string {
	return "deliverable-job-" + strconv.FormatInt(c.ID, 10) + "-attempt-" + strconv.Itoa(c.Attempt)
}

// What a claim found to do.
type claimOutcome int

const (
	// claimNothing: not this message's job to run. The row is gone, done,
	// another attempt's, or running under a worker that is alive.
	claimNothing claimOutcome = iota
	// claimRun: the attempt is this worker's to make.
	claimRun
	// claimFinishCancel: a cancel was asked for and the worker that should
	// have stopped is gone; the attempt is to be marked cancelled.
	claimFinishCancel
)

// Claim is a claim's answer.
type Claim struct {
	Outcome claimOutcome
	Job     Claimed
	// Why says what a claimNothing found, for the log.
	Why string
}

// ClaimRules decide when an attempt whose worker stopped is taken over.
type ClaimRules struct {
	// StaleAfter is how long a running attempt may go without a heartbeat
	// before its worker is taken to be gone. It must be several keep-alive
	// intervals.
	StaleAfter time.Duration
	// MaxTakeovers is how many times one attempt may be taken over. An
	// attempt whose worker keeps dying (out of memory on every render) must
	// end rather than loop.
	MaxTakeovers int
}

// ErrGone is a job that no longer exists.
var ErrGone = errors.New("videojob: the job is gone")

// Rows is the job's database.
type Rows interface {
	// Claim takes the attempt the message names, if it is this message's to
	// run (see Claim's outcomes).
	Claim(ctx context.Context, jobID int64, taskID string, rules ClaimRules) (Claim, error)
	// Heartbeat records the phase and progress and that the worker lives.
	// It reports false, writing nothing, once the attempt is no longer
	// running under this task id.
	Heartbeat(ctx context.Context, job Claimed, phase string, progress int) (bool, error)
	// Touch records only that the worker lives: a render sits in one phase
	// for longer than a takeover waits.
	Touch(ctx context.Context, job Claimed) error
	// State is the job's status and task id now, or ErrGone.
	State(ctx context.Context, job Claimed) (status, taskID string, err error)
	// Fail marks a running attempt failed. One whose cancel was asked for
	// meanwhile is marked cancelled instead: the person asked for that.
	Fail(ctx context.Context, job Claimed, code, internal string) error
	// Cancelled finishes a cancel: cancelling becomes cancelled.
	Cancelled(ctx context.Context, job Claimed) error
	// Release gives a running attempt back to the queue, for a worker that
	// is stopping: the message goes back too, and whoever takes it next
	// claims the attempt as if it were new.
	Release(ctx context.Context, job Claimed) error
	// ReserveArtifactID is an id for the artifact, taken before its files
	// are stored so that their keys can name it.
	ReserveArtifactID(ctx context.Context) (int64, error)
	// Deliver writes the artifact and marks the attempt ready, in one
	// transaction, under a lock on the job's row. It reports false, and
	// writes nothing, when the attempt is no longer running under this
	// task id: a cancel that landed during the render is never written
	// over.
	Deliver(ctx context.Context, job Claimed, artifact artifactsave.Rows) (artifactsave.Created, bool, error)
}

// Store is Rows over Postgres.
type Store struct {
	Pool *pgxpool.Pool
}

// takeoversKey is where an attempt's takeover count rides in the row's
// checkpoint column.
const takeoversKey = "orphan_takeovers"

func (s Store) Claim(ctx context.Context, jobID int64, taskID string, rules ClaimRules) (Claim, error) {
	var claim Claim
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var (
			job        Claimed
			status     string
			stored     string
			checkpoint []byte
			artifactID *int64
			stale      bool
		)
		err := tx.QueryRow(ctx, `
			SELECT user_id::text, kind, title, tool_call_id, thread_id, status::text, COALESCE(celery_task_id, ''),
			       attempt_count, request, checkpoint, artifact_id,
			       heartbeat_at IS NULL OR heartbeat_at < now() - make_interval(secs => $2)
			FROM deliverable_jobs WHERE id = $1 FOR UPDATE`, jobID, rules.StaleAfter.Seconds()).Scan(
			&job.UserID, &job.Kind, &job.Title, &job.ToolCallID, &job.ThreadID, &status, &stored,
			&job.Attempt, &job.Request, &checkpoint, &artifactID, &stale)
		if errors.Is(err, pgx.ErrNoRows) {
			claim.Why = "the job is gone"
			return nil
		}
		if err != nil {
			return err
		}
		job.ID, job.TaskID = jobID, stored
		var counts map[string]any
		_ = json.Unmarshal(checkpoint, &counts)
		if number, isNumber := counts[takeoversKey].(float64); isNumber {
			job.Takeovers = int(number)
		}
		switch {
		case taskID == "" || stored != taskID:
			// A message of an earlier attempt, delivered late: the job was
			// retried since, and that attempt is someone else's.
			claim.Why = fmt.Sprintf("the message is for task %q and the job is at %q", taskID, stored)
			return nil
		case artifactID != nil:
			claim.Why = "the job already has its video"
			return nil
		case status == "queued":
			_, err := tx.Exec(ctx, `
				UPDATE deliverable_jobs
				SET status = 'running', phase = 'starting', progress = 0, claimed_at = now(), heartbeat_at = now(),
				    failure_code = NULL, internal_error = NULL, updated_at = now()
				WHERE id = $1`, jobID)
			claim = Claim{Outcome: claimRun, Job: job}
			return err
		case status == "running" && !stale:
			// The same message delivered twice while its first delivery is
			// still being made.
			claim.Why = "the attempt is running under a worker that is alive"
			return nil
		case status == "running" && job.Takeovers >= rules.MaxTakeovers:
			_, err := tx.Exec(ctx, `
				UPDATE deliverable_jobs
				SET status = 'failed', phase = 'failed', failure_code = $2, internal_error = $3,
				    heartbeat_at = now(), finished_at = now(), updated_at = now()
				WHERE id = $1`, jobID, codeRender,
				fmt.Sprintf("the worker making this video stopped answering %d times; it is not taken over again", job.Takeovers+1))
			claim.Why = "the attempt's worker stopped answering too often; marked failed"
			return err
		case status == "running":
			job.Takeovers++
			_, err := tx.Exec(ctx, `
				UPDATE deliverable_jobs
				SET phase = 'starting', progress = 0, claimed_at = now(), heartbeat_at = now(), updated_at = now(),
				    checkpoint = jsonb_set(checkpoint, '{`+takeoversKey+`}', to_jsonb($2::int))
				WHERE id = $1`, jobID, job.Takeovers)
			claim = Claim{Outcome: claimRun, Job: job}
			return err
		case status == "cancelling" && stale:
			claim = Claim{Outcome: claimFinishCancel, Job: job}
			return nil
		default:
			claim.Why = "the job is " + status
			return nil
		}
	})
	return claim, err
}

func (s Store) Heartbeat(ctx context.Context, job Claimed, phase string, progress int) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE deliverable_jobs SET phase = $4, progress = $5, heartbeat_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid AND celery_task_id = $3 AND status = 'running'`,
		job.ID, job.UserID, job.TaskID, phase, progress)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s Store) Touch(ctx context.Context, job Claimed) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE deliverable_jobs SET heartbeat_at = now()
		WHERE id = $1 AND user_id = $2::uuid AND celery_task_id = $3 AND status = 'running'`,
		job.ID, job.UserID, job.TaskID)
	return err
}

func (s Store) State(ctx context.Context, job Claimed) (string, string, error) {
	var status, taskID string
	err := s.Pool.QueryRow(ctx, `
		SELECT status::text, COALESCE(celery_task_id, '') FROM deliverable_jobs WHERE id = $1 AND user_id = $2::uuid`,
		job.ID, job.UserID).Scan(&status, &taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrGone
	}
	return status, taskID, err
}

func (s Store) Fail(ctx context.Context, job Claimed, code, internal string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE deliverable_jobs
		SET status = CASE WHEN status = 'cancelling' THEN 'cancelled'::deliverable_job_status ELSE 'failed' END,
		    phase = CASE WHEN status = 'cancelling' THEN 'cancelled' ELSE 'failed' END,
		    failure_code = CASE WHEN status = 'cancelling' THEN 'cancelled' ELSE $4 END,
		    internal_error = CASE WHEN status = 'cancelling' THEN NULL ELSE $5 END,
		    heartbeat_at = now(), finished_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid AND celery_task_id = $3
		  AND status IN ('running', 'cancelling') AND artifact_id IS NULL`,
		job.ID, job.UserID, job.TaskID, code, internal)
	return err
}

func (s Store) Cancelled(ctx context.Context, job Claimed) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE deliverable_jobs
		SET status = 'cancelled', phase = 'cancelled', failure_code = 'cancelled', internal_error = NULL,
		    heartbeat_at = now(), finished_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid AND celery_task_id = $3 AND status = 'cancelling' AND artifact_id IS NULL`,
		job.ID, job.UserID, job.TaskID)
	return err
}

func (s Store) Release(ctx context.Context, job Claimed) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE deliverable_jobs
		SET status = 'queued', phase = NULL, progress = 0, claimed_at = NULL, heartbeat_at = NULL,
		    internal_error = 'the worker stopped while making this video; it was put back on the queue',
		    updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid AND celery_task_id = $3 AND status = 'running' AND artifact_id IS NULL`,
		job.ID, job.UserID, job.TaskID)
	return err
}

func (s Store) ReserveArtifactID(ctx context.Context) (int64, error) {
	return artifactsave.ReserveID(ctx, s.Pool)
}

func (s Store) Deliver(ctx context.Context, job Claimed, artifact artifactsave.Rows) (artifactsave.Created, bool, error) {
	var (
		created   artifactsave.Created
		delivered bool
	)
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		// The lock is held to the commit. A cancel either lands first, and
		// is seen here, or waits for the commit and finds the job ready,
		// which its own guarded statement then leaves alone.
		var (
			status, taskID string
			artifactID     *int64
			threadID       *int64
		)
		err := tx.QueryRow(ctx, `
			SELECT status::text, COALESCE(celery_task_id, ''), artifact_id, thread_id
			FROM deliverable_jobs WHERE id = $1 AND user_id = $2::uuid FOR UPDATE`,
			job.ID, job.UserID).Scan(&status, &taskID, &artifactID, &threadID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status != "running" || taskID != job.TaskID || artifactID != nil {
			return nil
		}
		// The thread as it is now: deleted while the video was made, the
		// video is still the account's, in no conversation.
		artifact.ThreadID = threadID
		if created, err = artifactsave.Insert(ctx, tx, artifact); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE deliverable_jobs
			SET status = 'ready', phase = 'ready', progress = 100, artifact_id = $3,
			    failure_code = NULL, internal_error = NULL,
			    heartbeat_at = now(), finished_at = now(), updated_at = now()
			WHERE id = $1 AND user_id = $2::uuid`, job.ID, job.UserID, created.ArtifactID); err != nil {
			return err
		}
		delivered = true
		return nil
	})
	if err != nil {
		return artifactsave.Created{}, false, err
	}
	return created, delivered, nil
}
