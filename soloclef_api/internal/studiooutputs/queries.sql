-- The statements of a notebook's studio outputs. Each names the owner, and
-- every join to a run or an artifact names it again, so an output can never
-- show another account's run even if a row pointed at one.

-- name: NotebookIsOwned :one
SELECT EXISTS (SELECT 1 FROM notebooks WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id));

-- name: Outputs :many
-- A notebook's outputs, newest first, each with the state of the run making
-- it, read from the run itself so the list never drifts from the truth.
-- `only_id` narrows the list to one output (0 is every output). A run's
-- status is read as jsonb because the joins are outer: a missing run is
-- SQL NULL, which a text column of the generated row could not hold.
SELECT o.id, o.kind, o.title, o.options, o.source_document_ids, o.generated_by, o.created_at,
       to_jsonb(p.status) AS podcast_status, p.duration_seconds AS podcast_duration, p.artifact_id AS podcast_artifact,
       to_jsonb(j.status) AS job_status, j.phase AS job_phase, j.progress AS job_progress,
       j.failure_code AS job_failure, j.artifact_id AS job_artifact,
       a.format AS artifact_format, a.metadata AS artifact_metadata
FROM notebook_outputs o
LEFT JOIN podcast_runs p ON p.id = o.podcast_id AND p.user_id = o.user_id
LEFT JOIN deliverable_jobs j ON j.id = o.deliverable_job_id AND j.user_id = o.user_id
LEFT JOIN artifacts a ON a.id = COALESCE(p.artifact_id, j.artifact_id) AND a.user_id = o.user_id
WHERE o.notebook_id = sqlc.arg(notebook_id) AND o.user_id = sqlc.arg(user_id)
  AND (sqlc.arg(only_id)::bigint = 0 OR o.id = sqlc.arg(only_id)::bigint)
ORDER BY o.created_at DESC, o.id DESC;

-- name: RenameOutput :execrows
UPDATE notebook_outputs SET title = sqlc.arg(title)
WHERE id = sqlc.arg(id) AND notebook_id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- name: DeleteOutput :one
-- The output, removed, answering the runs it pointed at so the delete can
-- stop them and remove what they made.
DELETE FROM notebook_outputs
WHERE id = sqlc.arg(id) AND notebook_id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id)
RETURNING podcast_id, deliverable_job_id;

-- name: CancelQueuedJob :one
-- A job no worker has taken is cancelled outright; one being worked on is
-- asked to stop (the worker reads `cancelling`). Answers the job's artifact.
UPDATE deliverable_jobs SET
    status = CASE WHEN status = 'queued' THEN 'cancelled'::deliverable_job_status ELSE 'cancelling'::deliverable_job_status END,
    phase = CASE WHEN status = 'queued' THEN 'cancelled' ELSE 'cancelling' END,
    failure_code = CASE WHEN status = 'queued' THEN 'cancelled' ELSE failure_code END,
    finished_at = CASE WHEN status = 'queued' THEN sqlc.arg(now)::timestamptz ELSE finished_at END,
    cancel_requested_at = sqlc.arg(now)::timestamptz,
    updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status IN ('queued', 'running')
RETURNING artifact_id;

-- name: JobArtifact :one
SELECT artifact_id FROM deliverable_jobs WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: CancelPodcast :exec
-- A podcast not yet finished is cancelled; a finished one is left as it is.
UPDATE podcast_runs SET status = 'cancelled'
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status NOT IN ('ready', 'failed', 'cancelled');

-- name: PodcastArtifact :one
SELECT artifact_id FROM podcast_runs WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: MarkArtifactDocumentDeleting :one
-- The document the artifact stands on, claimed for removal: out of search
-- at once, and the removal can be repeated from here.
UPDATE documents d SET status = jsonb_build_object('state', 'deleting')
FROM artifacts a
WHERE a.id = sqlc.arg(artifact_id) AND a.user_id = sqlc.arg(user_id)
  AND d.id = a.document_id AND d.user_id = a.user_id
RETURNING d.id;
