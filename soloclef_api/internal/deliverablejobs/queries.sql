-- A deliverable job of the caller's: read, asked to stop, and tried again.
-- Each names the owner, so another account's job is the same 404 as one
-- never made.

-- name: Job :one
SELECT id, kind, title, status::text AS status, phase, progress, failure_code,
       artifact_id, thread_id, created_at, updated_at
FROM deliverable_jobs
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: CancelJob :one
-- A job no worker has taken is cancelled outright; one being worked on is
-- asked to stop, which its worker reads. A job that has already made its
-- artifact is finishing and is not stopped half-saved.
UPDATE deliverable_jobs SET
    status = CASE WHEN status = 'queued' THEN 'cancelled'::deliverable_job_status ELSE 'cancelling'::deliverable_job_status END,
    phase = CASE WHEN status = 'queued' THEN 'cancelled' ELSE 'cancelling' END,
    failure_code = CASE WHEN status = 'queued' THEN 'cancelled' ELSE failure_code END,
    finished_at = CASE WHEN status = 'queued' THEN sqlc.arg(now)::timestamptz ELSE finished_at END,
    cancel_requested_at = sqlc.arg(now)::timestamptz,
    updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status IN ('queued', 'running') AND artifact_id IS NULL
RETURNING id, kind, title, status::text AS status, phase, progress, failure_code,
          artifact_id, thread_id, created_at, updated_at;

-- name: RetryJob :one
-- A failed or cancelled job is queued again as its next attempt, in one
-- statement, so two retries at once cannot both advance it: the second
-- finds it queued and changes nothing. The new task id is the only one the
-- worker will claim the job for (soloclef_worker/internal/videojob), which
-- is what stops an old attempt's message, still on the queue, from running
-- it. The checkpoint is emptied because it belongs to the attempt that
-- ended: its takeover count must not be held against the new one.
--
-- updated_at is the outbox clock: the worker's reconciler republishes a
-- job that has sat queued past its threshold, so a publish that fails after
-- this commit is repaired from the row alone.
--
-- Only a video: it is the one kind a worker makes. A job with its artifact
-- is finished, whatever its status says, and is not made twice.
UPDATE deliverable_jobs SET
    status = 'queued',
    phase = NULL,
    progress = 0,
    checkpoint = '{}'::jsonb,
    attempt_count = attempt_count + 1,
    celery_task_id = 'deliverable-job:' || id || ':attempt:' || (attempt_count + 1),
    failure_code = NULL,
    internal_error = NULL,
    cancel_requested_at = NULL,
    claimed_at = NULL,
    heartbeat_at = NULL,
    finished_at = NULL,
    updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND kind = 'video'
  AND status IN ('failed', 'cancelled') AND artifact_id IS NULL
RETURNING id, kind, title, status::text AS status, phase, progress, failure_code,
          artifact_id, thread_id, created_at, updated_at, celery_task_id::text AS task_id;
