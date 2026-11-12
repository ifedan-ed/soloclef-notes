-- The statements of the podcast routes. A podcast is its owner's: every
-- statement that reads or writes one names `user_id` beside the id, and that
-- predicate is the whole of the access check.

-- name: PodcastOfOwner :one
-- One of the caller's podcasts by its id. Another account's is not found.
-- `source_content` -- the text the episode is drafted from, often the longest
-- value in the row -- is left out: no answer carries it. `status` is read as
-- text; the column's own type is the `podcast_status` enum.
SELECT id, title, status::text AS status, spec_version, spec,
       podcast_transcript, duration_seconds, error, artifact_id,
       thread_id, created_at
FROM podcast_runs
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: MovePodcast :execrows
-- One lifecycle transition, and only from the status the decision was made
-- on. Two requests that race for the same transition cannot both win: the
-- second matches no row and is told the podcast changed, instead of writing
-- the same status again and enqueueing a second draft.
UPDATE podcast_runs
SET status = sqlc.arg(target)::text::podcast_status
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status = sqlc.arg(current)::text::podcast_status;

-- name: DeletePodcast :execrows
-- The row and nothing else: the episode's artifact is not this row's to
-- remove, and a notebook output that named the podcast is set to NULL by its
-- own foreign key.
DELETE FROM podcast_runs WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: CreatePodcast :one
-- A new podcast, already at the brief gate with the brief proposed for it.
-- The database mints `created_at`. The columns answered are PodcastOfOwner's,
-- in its order.
INSERT INTO podcast_runs (title, status, source_content, spec, spec_version,
                          user_id, thread_id)
VALUES (sqlc.arg(title), 'awaiting_brief'::podcast_status,
        sqlc.arg(source_content), sqlc.arg(spec), 1,
        sqlc.arg(user_id), sqlc.narg(thread_id))
RETURNING id, title, status::text AS status, spec_version, spec,
          podcast_transcript, duration_seconds, error, artifact_id,
          thread_id, created_at;

-- name: LatestBrief :one
-- The brief of the caller's most recent podcast that has one: what a new
-- podcast's language and voices default to. A jsonb `null` is a value, not an
-- absence, so the type is asked for as well; and two podcasts made in the
-- same microsecond are ordered by id, so the answer is always the same row.
SELECT spec
FROM podcast_runs
WHERE user_id = sqlc.arg(user_id)
  AND spec IS NOT NULL
  AND jsonb_typeof(spec) = 'object'
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ThreadOfOwner :one
-- Whether the thread a new podcast names is one of the caller's own. The
-- column's foreign key only says the thread exists somewhere.
SELECT EXISTS (
    SELECT 1 FROM new_chat_threads
    WHERE id = sqlc.arg(thread_id) AND user_id = sqlc.arg(user_id)
);

-- name: ReplaceBrief :execrows
-- An edit of the brief, and only of the brief that was checked: still at the
-- gate, still at the version the editor saw. Two edits that race cannot both
-- win; the second matches no row.
UPDATE podcast_runs
SET spec = sqlc.arg(spec), spec_version = spec_version + 1
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status = 'awaiting_brief'::podcast_status
  AND spec_version = sqlc.arg(expected_version);
