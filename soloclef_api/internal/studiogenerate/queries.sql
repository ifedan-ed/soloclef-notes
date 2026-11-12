-- Starting a studio output. Every statement names the owner; a notebook, a
-- source, a document or a job of another account is a row that is not there.

-- name: NotebookTitle :one
SELECT title FROM notebooks WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- name: SuggestionCache :one
-- The notebook's cached suggestion pools and the source set they were
-- written for.
SELECT focus_suggestions, focus_suggestions_key FROM notebooks
WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- name: StoreSuggestionPool :exec
-- One pool, kept beside the others written for the same source set. A pool
-- written for a different source set replaces the whole cache: its ideas
-- name things the notebook may no longer hold.
UPDATE notebooks SET
    focus_suggestions = CASE WHEN focus_suggestions_key = sqlc.arg(sources_key) AND jsonb_typeof(focus_suggestions) = 'object'
                             THEN focus_suggestions ELSE '{}'::jsonb END
                        || jsonb_build_object(sqlc.arg(pool_key)::text, sqlc.arg(pool)::jsonb),
    focus_suggestions_key = sqlc.arg(sources_key)
WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- name: ChosenSources :many
-- The notebook's sources a generation reads: the ticked ones, or exactly
-- the ones named. The caller compares the count with the ids it named, so
-- an id that is not this notebook's refuses the whole request.
SELECT id, document_id, folder_id FROM notebook_sources
WHERE notebook_id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id)
  AND CASE WHEN sqlc.arg(only_enabled)::boolean THEN enabled
           ELSE id = ANY(sqlc.arg(source_ids)::bigint[]) END;

-- name: SourceDocuments :many
-- The documents those sources reach: each named document and every document
-- directly in a named folder, the caller's own, in a stable order. One being
-- deleted is left out: its text is on its way out of the account.
SELECT id, title, COALESCE(NULLIF(source_markdown, ''), content) AS body
FROM documents
WHERE user_id = sqlc.arg(user_id)
  AND (id = ANY(sqlc.arg(document_ids)::bigint[]) OR folder_id = ANY(sqlc.arg(folder_ids)::bigint[]))
  AND status ->> 'state' <> 'deleting'
ORDER BY id
LIMIT sqlc.arg(most);

-- name: OutputForToolCall :one
-- The output an earlier click with the same request id started: by the key
-- the output itself keeps (an audio overview, which has no job), or by the
-- job it points at (every other kind, and outputs older than the key).
SELECT o.id FROM notebook_outputs o
LEFT JOIN deliverable_jobs j ON j.id = o.deliverable_job_id AND j.user_id = o.user_id
WHERE o.notebook_id = sqlc.arg(notebook_id) AND o.user_id = sqlc.arg(user_id)
  AND (o.request_key = sqlc.arg(tool_call_id)::text
       OR (md5(j.tool_call_id) = md5(sqlc.arg(tool_call_id)::text) AND j.tool_call_id = sqlc.arg(tool_call_id)::text))
ORDER BY o.id
LIMIT 1;

-- name: CreateStudioThread :one
-- A generation's own hidden thread. The sandbox and an artifact's
-- attribution are keyed by thread, so two generations must not share one;
-- `artifact_api` keeps it out of every list of conversations.
INSERT INTO new_chat_threads (user_id, title, notebook_id, source)
SELECT n.user_id, sqlc.arg(title), n.id, 'artifact_api'
FROM notebooks n WHERE n.id = sqlc.arg(notebook_id) AND n.user_id = sqlc.arg(user_id)
RETURNING id;

-- name: CreateArtifactJob :one
-- The row a studio output's progress is read from, for an artifact this
-- process makes itself. Nothing is published for it: no worker claims the
-- kind `artifact`. A second click with the same request id inserts nothing.
INSERT INTO deliverable_jobs (user_id, kind, title, thread_id, tool_call_id, request, status, phase)
SELECT t.user_id, 'artifact', sqlc.arg(title), t.id, sqlc.arg(tool_call_id)::text, sqlc.arg(request)::jsonb, 'queued', 'queued'
FROM new_chat_threads t WHERE t.id = sqlc.arg(thread_id) AND t.user_id = sqlc.arg(user_id)
ON CONFLICT (user_id, kind, md5(tool_call_id)) DO NOTHING
RETURNING id;

-- name: CreateOutput :one
-- The output, pointing at the run that makes it. The notebook is read under
-- the owner, so an output is never added to another account's notebook, and
-- a notebook used is a notebook moved to the top of the list.
WITH touched AS (
    UPDATE notebooks AS n SET updated_at = now()
    WHERE n.id = sqlc.arg(notebook_id) AND n.user_id = sqlc.arg(user_id)
    RETURNING n.id, n.user_id
)
INSERT INTO notebook_outputs (notebook_id, user_id, kind, title, options, source_document_ids,
                              podcast_id, deliverable_job_id, thread_id, generated_by, request_key)
SELECT touched.id, touched.user_id, sqlc.arg(kind), sqlc.arg(title), sqlc.arg(options)::jsonb,
       sqlc.arg(source_document_ids)::jsonb, sqlc.narg(podcast_id), sqlc.narg(deliverable_job_id),
       sqlc.narg(thread_id), sqlc.narg(generated_by)::jsonb, sqlc.narg(request_key)
FROM touched
RETURNING notebook_outputs.id;

-- name: ClaimJob :execrows
-- This process takes the job it queued. A job cancelled while it waited
-- for a place matches nothing, and is not run.
UPDATE deliverable_jobs SET status = 'running', phase = 'generating', claimed_at = sqlc.arg(now)::timestamptz,
       heartbeat_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND kind = 'artifact' AND status = 'queued';

-- name: Heartbeat :one
-- "Still being made", and the answer says whether the person has asked for
-- it to stop in the meantime (`cancelling`).
UPDATE deliverable_jobs SET heartbeat_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status IN ('queued', 'running', 'cancelling')
RETURNING status::text;

-- name: FinishJob :execrows
-- How a run ended, written only over a run still in progress: a job a
-- person cancelled outright stays cancelled. `ready` names the artifact,
-- which is read under the owner so a job can never point at another's.
UPDATE deliverable_jobs j SET
    status = sqlc.arg(status)::deliverable_job_status,
    phase = sqlc.arg(phase), progress = sqlc.arg(progress),
    failure_code = sqlc.narg(failure_code), internal_error = sqlc.narg(internal_error),
    artifact_id = (SELECT a.id FROM artifacts a WHERE a.id = sqlc.narg(artifact_id) AND a.user_id = j.user_id),
    finished_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)::timestamptz
WHERE j.id = sqlc.arg(id) AND j.user_id = sqlc.arg(user_id) AND j.status IN ('queued', 'running', 'cancelling');

-- name: ArtifactOfThread :one
-- What the run saved: the newest artifact of the generation's own thread.
SELECT id FROM artifacts
WHERE user_id = sqlc.arg(user_id) AND thread_id = sqlc.arg(thread_id)
ORDER BY id DESC LIMIT 1;

-- name: FailAbandonedJobs :execrows
-- Artifact jobs whose process went away: in progress, and silent for longer
-- than a live run ever is. They are failed so the panel stops spinning.
-- Deliberately not owner-keyed: it is this tier's own housekeeping, run on
-- a timer for every account, and it names the one kind this tier runs.
UPDATE deliverable_jobs SET
    status = CASE WHEN status = 'cancelling' THEN 'cancelled' ELSE 'failed' END::deliverable_job_status,
    phase = CASE WHEN status = 'cancelling' THEN 'cancelled' ELSE 'failed' END,
    failure_code = CASE WHEN status = 'cancelling' THEN 'cancelled' ELSE 'interrupted' END,
    finished_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)::timestamptz
WHERE kind = 'artifact' AND status IN ('queued', 'running', 'cancelling')
  AND COALESCE(heartbeat_at, updated_at) < sqlc.arg(silent_since)::timestamptz;

-- name: CancelUnstartedPodcast :exec
-- A podcast opened for a studio output whose draft could not be queued: it
-- is closed, so nothing waits at a brief gate no card will ever show.
UPDATE podcast_runs SET status = 'cancelled'
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status = 'awaiting_brief';

-- name: DocumentsStillInNotebook :many
-- Of the documents named, the caller's own that one of this notebook's
-- sources still reaches (the document itself, or the folder it is in),
-- ticked or not: what an output started from a report reads, which is what
-- the report was made from and not what happens to be ticked today. One
-- being deleted is left out, as above.
SELECT d.id, d.title, COALESCE(NULLIF(d.source_markdown, ''), d.content) AS body
FROM documents d
WHERE d.user_id = sqlc.arg(user_id)
  AND d.id = ANY(sqlc.arg(document_ids)::bigint[])
  AND d.status ->> 'state' <> 'deleting'
  AND EXISTS (
      SELECT 1 FROM notebook_sources s
      WHERE s.notebook_id = sqlc.arg(notebook_id) AND s.user_id = d.user_id
        AND (s.document_id = d.id OR s.folder_id = d.folder_id))
ORDER BY d.id
LIMIT sqlc.arg(most);
