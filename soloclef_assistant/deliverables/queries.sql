-- Queueing a video: the job row the render worker claims. The rows an
-- artifact is saved as are soloclef_studio/artifactsave's, which this
-- package saves through (artifactsave.go).

-- name: CreateVideoJob :one
-- A queued video: the row the render worker claims and the page's video
-- card follows. The id is taken first so that the row can name, in the same
-- statement, the one task id allowed to claim it ("deliverable-job:<id>:
-- attempt:1"); the message is published under that id after the commit.
--
-- A second run of the same tool call (a turn continued after a restart)
-- inserts nothing and returns no row: the unique index on (account, kind,
-- md5(tool_call_id)) is the job's idempotency key.
--
-- The thread is taken from new_chat_threads under the owner, not from the
-- argument alone: a job can only ever name a thread of the account it
-- belongs to, and a thread that is not the account's inserts nothing.
WITH next AS (
    SELECT nextval('deliverable_jobs_id_seq'::regclass) AS id
)
INSERT INTO deliverable_jobs (
    id, kind, title, user_id, thread_id, tool_call_id,
    request, checkpoint, celery_task_id
)
SELECT next.id, 'video', sqlc.arg(title), thread.user_id, thread.id,
       sqlc.arg(tool_call_id)::text,
       -- root_thread_id is in the request as well as in the row: the worker
       -- refuses a job whose two do not agree.
       sqlc.arg(request)::jsonb, '{}'::jsonb,
       'deliverable-job:' || next.id || ':attempt:1'
FROM next
JOIN new_chat_threads AS thread
  ON thread.id = sqlc.arg(thread_id) AND thread.user_id = sqlc.arg(user_id)
ON CONFLICT (user_id, kind, md5(tool_call_id)) DO NOTHING
RETURNING id, celery_task_id;

-- name: VideoJobForToolCall :one
-- The job an earlier run of this tool call queued. md5 is compared first so
-- the unique index answers; the text is compared as well because two ids
-- can share an md5.
SELECT id, title, thread_id, status::text AS status, celery_task_id
FROM deliverable_jobs
WHERE user_id = sqlc.arg(user_id) AND kind = 'video'
  AND md5(tool_call_id) = md5(sqlc.arg(tool_call_id)::text)
  AND tool_call_id = sqlc.arg(tool_call_id)::text;

-- name: LatestPodcastBrief :one
-- The brief of the account's most recent podcast that has one: what a new
-- podcast's language and voices default to. A jsonb `null` is a value, not
-- an absence, so the type is asked for as well; and two podcasts made in
-- the same microsecond are ordered by id, so the answer is always the same
-- row.
SELECT spec
FROM podcast_runs
WHERE user_id = sqlc.arg(user_id)
  AND spec IS NOT NULL
  AND jsonb_typeof(spec) = 'object'
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: CreatePodcast :one
-- A new podcast, already at the brief gate with the brief proposed for it.
-- The thread is taken from new_chat_threads under the owner, as a video
-- job's is: a podcast can only name a thread of the account it belongs to,
-- and a thread that is not the account's inserts nothing.
INSERT INTO podcast_runs (title, status, source_content, spec, spec_version,
                          user_id, thread_id)
SELECT sqlc.arg(title), 'awaiting_brief'::podcast_status,
       sqlc.arg(source_content)::text, sqlc.arg(spec)::jsonb, 1,
       thread.user_id, thread.id
FROM new_chat_threads AS thread
WHERE thread.id = sqlc.arg(thread_id) AND thread.user_id = sqlc.arg(user_id)
RETURNING id;
