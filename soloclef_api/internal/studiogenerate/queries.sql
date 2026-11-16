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
-- deleted is left out: its text is on its way out of the account. `is_read`
-- says the document has been read (state "ready"): one still being read has
-- no text yet. `content_hash` changes when a document is read again.
SELECT id, title, COALESCE(NULLIF(source_markdown, ''), content) AS body,
       (status ->> 'state') = 'ready' AS is_read, content_hash
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

-- name: LockAccountStudioJobs :exec
-- One account's Studio requests are counted one at a time, for the rest of
-- the transaction that queues the job, so two clicks at once cannot both be
-- counted under the limit (UnfinishedArtifactJobs) and both be queued. Per
-- account: another account's request never waits on it.
SELECT pg_advisory_xact_lock(hashtextextended('soloclef studiogenerate account ' || sqlc.arg(user_id)::text, 0));

-- name: UnfinishedArtifactJobs :one
-- How many of the account's artifact jobs are waiting or being made: what
-- the per-account limit at the generate route counts.
SELECT count(*) FROM deliverable_jobs
WHERE user_id = sqlc.arg(user_id) AND kind = 'artifact' AND status IN ('queued', 'running', 'cancelling');

-- name: CreateArtifactJob :one
-- The row a studio output's progress is read from, for an artifact this
-- process makes itself. Nothing is published for it: no worker claims the
-- kind `artifact`. A second click with the same request id inserts nothing.
INSERT INTO deliverable_jobs (user_id, kind, title, thread_id, tool_call_id, request, status, phase)
SELECT t.user_id, 'artifact', sqlc.arg(title), t.id, sqlc.arg(tool_call_id)::text, sqlc.arg(request)::jsonb, 'queued', 'queued'
FROM new_chat_threads t WHERE t.id = sqlc.arg(thread_id) AND t.user_id = sqlc.arg(user_id)
ON CONFLICT (user_id, kind, md5(tool_call_id)) DO NOTHING
RETURNING id;

-- The output row itself is written by soloclef_studio/notebookoutput, the
-- one statement a notebook's chat lists what it makes with too.

-- An artifact job's life, as this tier runs it (runner.go, requeue.go):
--
--   queued -> running -> ready | failed | cancelled
--      ^         |
--      +---------+  cut off (a shutdown that outlasted its drain, or a
--                   process that died), with attempts left: attempt + 1
--
-- The row is the queue. No process holds a queued job: whichever API replica
-- has a free place claims it, and every write after the claim names the
-- attempt it was claimed as (attempt_count), so a process that lost its job
-- to a re-queue finds every later write of its own refused.

-- name: LockStudioClaims :exec
-- Claims run one at a time across every replica, for the rest of the
-- claiming transaction. The per-account limit below counts the account's
-- running jobs, and two claims at once would each count before the other
-- had committed and could together start more than the limit. A claim is
-- one short statement made when a run ends or a job is queued, so waiting
-- for another replica's is milliseconds. The lock is taken in a statement
-- of its own because a statement's snapshot is taken as it starts: the
-- claim that follows, under READ COMMITTED, sees every claim committed
-- before the lock was granted.
SELECT pg_advisory_xact_lock(hashtextextended('soloclef studiogenerate claim', 0));

-- name: ClaimQueuedJobs :many
-- The queued artifact jobs to start now, up to the places this process has
-- free, fairly: no account has more than per_account jobs running at once
-- (counting the ones running in every replica, and the ones being stopped,
-- which still hold their place), and the places go round the accounts. Each
-- account's oldest waiting job comes first, the accounts ordered by how long
-- that job has waited; then each account's second, and so on. So a person
-- who queued twenty outputs has two made at a time, and someone who queues
-- one after them has it started at the next free place, not after the
-- twenty.
--
-- FOR UPDATE SKIP LOCKED is still the rule that keeps two replicas from
-- taking the same job (LockStudioClaims also serialises them; the row lock
-- is what keeps a claim from waiting on a row a cancel or the sweep holds):
-- a locked row is skipped, not waited for, and one already moved to
-- running no longer matches status = 'queued'. MATERIALIZED, so the choice
-- is made once and LIMIT bounds it (jobreconcile's TakeStale says what
-- happens otherwise). A job with no stored task cannot be made here and is
-- left for the sweep to close.
WITH running AS (
    SELECT d.user_id, count(*) AS runs FROM deliverable_jobs AS d
    WHERE d.kind = 'artifact' AND d.status IN ('running', 'cancelling')
    GROUP BY d.user_id
),
waiting AS (
    SELECT d.id, d.user_id, d.created_at,
           row_number() OVER (PARTITION BY d.user_id ORDER BY d.created_at, d.id) AS place,
           min(d.created_at) OVER (PARTITION BY d.user_id) AS waiting_since
    FROM deliverable_jobs AS d
    WHERE d.kind = 'artifact' AND d.status = 'queued' AND d.request ->> 'task' IS NOT NULL
),
fair AS (
    SELECT w.id FROM waiting AS w
    LEFT JOIN running AS r ON r.user_id = w.user_id
    WHERE w.place + COALESCE(r.runs, 0) <= sqlc.arg(per_account)::bigint
    ORDER BY w.place, w.waiting_since, w.user_id, w.created_at, w.id
    LIMIT sqlc.arg(most)
),
next AS MATERIALIZED (
    SELECT d.id FROM deliverable_jobs AS d
    WHERE d.id IN (SELECT fair.id FROM fair) AND d.status = 'queued'
    FOR UPDATE SKIP LOCKED
)
UPDATE deliverable_jobs AS j SET status = 'running', phase = 'generating', claimed_at = sqlc.arg(now)::timestamptz,
       heartbeat_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)::timestamptz
FROM next
WHERE j.id = next.id
RETURNING j.id, j.user_id, j.thread_id, j.attempt_count, j.request;

-- name: Heartbeat :one
-- "Still being made" by this attempt, and the answer says whether the
-- person has asked for it to stop in the meantime (`cancelling`). No row
-- means the job is no longer this attempt's to make: cancelled outright,
-- deleted, or queued again by a sweep that took this process for dead.
UPDATE deliverable_jobs SET heartbeat_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND attempt_count = sqlc.arg(attempt)::int
  AND status IN ('running', 'cancelling')
RETURNING status::text;

-- name: FinishJob :execrows
-- How an attempt ended, written only over that attempt still in progress: a
-- job a person cancelled outright stays cancelled, and an attempt that was
-- superseded writes nothing. `ready` names the artifact, which is read under
-- the owner so a job can never point at another's. The stored task goes:
-- it is a copy of the person's sources, kept only while the job may still
-- have to be made again.
UPDATE deliverable_jobs j SET
    status = sqlc.arg(status)::deliverable_job_status,
    phase = sqlc.arg(phase), progress = sqlc.arg(progress),
    failure_code = sqlc.narg(failure_code), internal_error = sqlc.narg(internal_error),
    artifact_id = (SELECT a.id FROM artifacts a WHERE a.id = sqlc.narg(artifact_id) AND a.user_id = j.user_id),
    request = j.request - 'task',
    finished_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)::timestamptz
WHERE j.id = sqlc.arg(id) AND j.user_id = sqlc.arg(user_id) AND j.attempt_count = sqlc.arg(attempt)::int
  AND j.status IN ('running', 'cancelling');

-- name: ArtifactOfThread :one
-- What the run saved: the newest artifact of the generation's own thread.
SELECT id FROM artifacts
WHERE user_id = sqlc.arg(user_id) AND thread_id = sqlc.arg(thread_id)
ORDER BY id DESC LIMIT 1;

-- name: ReleaseJob :one
-- An attempt this process is stopping, because the server's drain ended
-- before it did: given back to the queue as the next attempt, for the next
-- process to take up; or, with no attempt left, failed as interrupted. A job
-- whose stop the person had asked for is cancelled instead, as they asked.
--
-- RequeueAbandonedJobs below does the same to the jobs of a process that
-- died. The two choose their rows differently (this one is one attempt of
-- one owner's job; that one is every silent job) and set them alike; keep
-- their SET lists the same.
WITH cut AS MATERIALIZED (
    SELECT d.id,
           CASE WHEN d.status = 'cancelling' THEN 'cancelled'
                WHEN d.attempt_count >= sqlc.arg(max_attempts)::int THEN 'failed'
                ELSE 'queued' END AS next
    FROM deliverable_jobs AS d
    WHERE d.id = sqlc.arg(id)::bigint AND d.user_id = sqlc.arg(user_id)::uuid AND d.kind = 'artifact'
      AND d.attempt_count = sqlc.arg(attempt)::int AND d.status IN ('running', 'cancelling')
    FOR UPDATE
)
UPDATE deliverable_jobs AS j SET
    status = cut.next::deliverable_job_status,
    phase = cut.next,
    progress = 0,
    failure_code = CASE cut.next WHEN 'queued' THEN NULL WHEN 'failed' THEN 'interrupted' ELSE 'cancelled' END,
    internal_error = CASE cut.next WHEN 'failed' THEN sqlc.arg(reason)::text END,
    attempt_count = j.attempt_count + CASE cut.next WHEN 'queued' THEN 1 ELSE 0 END,
    request = CASE cut.next WHEN 'queued' THEN j.request ELSE j.request - 'task' END,
    claimed_at = CASE cut.next WHEN 'queued' THEN NULL ELSE j.claimed_at END,
    heartbeat_at = CASE cut.next WHEN 'queued' THEN NULL ELSE j.heartbeat_at END,
    finished_at = CASE cut.next WHEN 'queued' THEN NULL ELSE sqlc.arg(now)::timestamptz END,
    updated_at = sqlc.arg(now)::timestamptz
FROM cut
WHERE j.id = cut.id
RETURNING j.status::text AS status, j.attempt_count;

-- name: RequeueAbandonedJobs :many
-- Artifact jobs whose process went away: in progress, and silent for longer
-- than a live run ever is (a running attempt beats every 20 seconds). Each
-- is queued again as its next attempt, or failed as interrupted once its
-- attempts are spent; one whose stop was asked for is cancelled. A queued
-- job holding no task (queued by a version of this tier that did not store
-- one) can never be claimed, and is failed the same way once it is as
-- silent.
--
-- Deliberately not owner-keyed: it is this tier's own housekeeping, run on a
-- timer in every replica for every account, and it names the one kind this
-- tier runs. SKIP LOCKED lets two replicas sweeping at once share the rows
-- instead of queueing behind each other, and the row lock is what makes the
-- attempt advance once: a second sweep that reaches the row after the first
-- finds it queued, fresh, and no longer silent. Each job it changed comes
-- back with its owner and new status, so a failed one is reported to them.
WITH cut AS MATERIALIZED (
    SELECT d.id,
           CASE WHEN d.status = 'cancelling' THEN 'cancelled'
                WHEN d.attempt_count >= sqlc.arg(max_attempts)::int OR d.request ->> 'task' IS NULL THEN 'failed'
                ELSE 'queued' END AS next
    FROM deliverable_jobs AS d
    WHERE d.kind = 'artifact'
      AND (d.status IN ('running', 'cancelling') OR (d.status = 'queued' AND d.request ->> 'task' IS NULL))
      AND COALESCE(d.heartbeat_at, d.updated_at) < sqlc.arg(silent_since)::timestamptz
    ORDER BY d.id
    LIMIT sqlc.arg(most)
    FOR UPDATE SKIP LOCKED
)
UPDATE deliverable_jobs AS j SET
    status = cut.next::deliverable_job_status,
    phase = cut.next,
    progress = 0,
    failure_code = CASE cut.next WHEN 'queued' THEN NULL WHEN 'failed' THEN 'interrupted' ELSE 'cancelled' END,
    internal_error = CASE cut.next WHEN 'failed' THEN sqlc.arg(reason)::text END,
    attempt_count = j.attempt_count + CASE cut.next WHEN 'queued' THEN 1 ELSE 0 END,
    request = CASE cut.next WHEN 'queued' THEN j.request ELSE j.request - 'task' END,
    claimed_at = CASE cut.next WHEN 'queued' THEN NULL ELSE j.claimed_at END,
    heartbeat_at = CASE cut.next WHEN 'queued' THEN NULL ELSE j.heartbeat_at END,
    finished_at = CASE cut.next WHEN 'queued' THEN NULL ELSE sqlc.arg(now)::timestamptz END,
    updated_at = sqlc.arg(now)::timestamptz
FROM cut
WHERE j.id = cut.id
RETURNING j.id, j.user_id, j.status::text AS status, j.attempt_count;

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

-- name: SetRunningPhase :exec
-- What a running job is doing now, for the panel: "waiting_for_sandbox"
-- while its account has no sandbox free, "generating" again once it has
-- one. Only over a run still running: a job asked to stop keeps
-- `cancelling`, and an ended one its ending.
-- And only by the attempt that is running: a process that lost its job to a
-- re-queue does not write over the phase of the attempt that replaced it.
UPDATE deliverable_jobs SET phase = sqlc.arg(phase), updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND kind = 'artifact' AND status = 'running'
  AND attempt_count = sqlc.arg(attempt)::int;

-- name: ForgetEndedConversations :execrows
-- The conversations of attempts that are not running, removed: each
-- attempt's conversation ("studio-job-<job>-attempt-<n>", attempt.go) holds
-- the task, which is the person's text, and only the attempt a job is
-- running now is ever read again. A run removes its own as it ends
-- (runner.go); this is what removes the rest, whoever ended the job: a
-- cancel of a queued job (deliverablejobs, studiooutputs), the sweep that
-- failed a job cut off on its last attempt, a job deleted with its
-- notebook or account. It runs with the sweep, so nothing is left for
-- longer than a minute after its job ends, and the routes that end a job
-- need not know how a conversation is named.
--
-- Deliberately not owner-keyed, like the sweep: a conversation row has no
-- owner column, and what decides is whether the job is still running. The
-- prefix range is what the table's text_pattern_ops index serves ('-' is
-- followed by '.' in byte order); the exact shape is checked after it, so a
-- name of any other kind is never touched. Migration 00028 removed, by the
-- same rule, those left before this ran.
DELETE FROM soloclef_agent_checkpoints AS c
WHERE c.thread_id IN (
    SELECT k.thread_id FROM soloclef_agent_checkpoints AS k
    WHERE k.thread_id ~>=~ 'studio-job-' AND k.thread_id ~<~ 'studio-job.'
      AND k.thread_id ~ '^studio-job-[0-9]{1,18}-attempt-[0-9]{1,9}(/|$)'
      AND NOT EXISTS (
          SELECT 1 FROM deliverable_jobs AS j
          WHERE j.kind = 'artifact' AND j.status IN ('running', 'cancelling')
            AND substring(k.thread_id FROM '^(studio-job-[0-9]+-attempt-[0-9]+)') = 'studio-job-' || j.id || '-attempt-' || j.attempt_count)
    LIMIT sqlc.arg(most)
);
