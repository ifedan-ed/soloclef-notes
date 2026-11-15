-- The statements of the one notification step: what an event is about,
-- read from the rows it names, and the inbox row it becomes. Every
-- statement names the owner, except the first read of a document, which is
-- how the owner of a document the indexer names by id is learned.

-- name: InsertNotification :one
-- A new inbox row, unread.
INSERT INTO notifications (user_id, type, title, message, read, metadata, created_at, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(type), sqlc.arg(title), sqlc.arg(message), false,
        sqlc.arg(metadata)::jsonb, sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz)
RETURNING id;

-- name: LockGroup :exec
-- Serialises the events of one collapsing group of one account until the
-- transaction ends, so two failures arriving together grow one row instead
-- of each starting its own. A transaction-scoped advisory lock: no table is
-- locked and nothing outlives the transaction.
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(user_id)::text || '/' || sqlc.arg(group_key)::text, 7310));

-- name: OpenGroup :one
-- The group's row still collecting: unread, and first written inside the
-- window. A row the person has read is closed, and the next event starts a
-- new one (and a new push).
SELECT id, metadata
FROM notifications
WHERE user_id = sqlc.arg(user_id)
  AND type = sqlc.arg(type)
  AND NOT read
  AND created_at > sqlc.arg(since)::timestamptz
  AND metadata ->> 'collapse_key' = sqlc.arg(group_key)::text
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;

-- name: GrowGroup :exec
-- The group's row, rewritten to stand for one more event. created_at stays
-- the first event's, so the window is measured from the start of the batch
-- and a steady trickle cannot keep one row open for ever.
UPDATE notifications
SET title = sqlc.arg(title), message = sqlc.arg(message), metadata = sqlc.arg(metadata)::jsonb,
    updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: PodcastOutput :one
-- What a podcast run made or failed to make, and where it lives: its
-- Studio output and notebook, or, for one a chat started, the chat's
-- notebook if it has one.
SELECT p.title AS run_title, p.thread_id,
       o.id AS output_id, o.kind AS output_kind, o.title AS output_title,
       nb.id AS notebook_id, nb.title AS notebook_title
FROM podcast_runs p
LEFT JOIN notebook_outputs o ON o.podcast_id = p.id AND o.user_id = p.user_id
LEFT JOIN new_chat_threads t ON t.id = p.thread_id AND t.user_id = p.user_id
LEFT JOIN notebooks nb ON nb.id = COALESCE(o.notebook_id, t.notebook_id) AND nb.user_id = p.user_id
WHERE p.id = sqlc.arg(podcast_id) AND p.user_id = sqlc.arg(user_id)
ORDER BY o.id DESC NULLS LAST
LIMIT 1;

-- name: DeliverableOutput :one
-- The same for a deliverable job: a Studio artifact or a video.
SELECT d.title AS run_title, d.kind AS run_kind, d.thread_id,
       o.id AS output_id, o.kind AS output_kind, o.title AS output_title,
       nb.id AS notebook_id, nb.title AS notebook_title
FROM deliverable_jobs d
LEFT JOIN notebook_outputs o ON o.deliverable_job_id = d.id AND o.user_id = d.user_id
LEFT JOIN new_chat_threads t ON t.id = d.thread_id AND t.user_id = d.user_id
LEFT JOIN notebooks nb ON nb.id = COALESCE(o.notebook_id, t.notebook_id) AND nb.user_id = d.user_id
WHERE d.id = sqlc.arg(job_id) AND d.user_id = sqlc.arg(user_id)
ORDER BY o.id DESC NULLS LAST
LIMIT 1;

-- name: SourceDocument :one
-- A document the indexer finished, with its owner, and the one notebook it
-- is a source of (0 when it is a source of none, or of several: a push can
-- open one notebook, and a guess between several would be a guess).
SELECT d.user_id, d.title, d.document_type::text AS document_type,
       (d.connector_id IS NOT NULL)::boolean AS synced,
       COALESCE((SELECT CASE WHEN count(DISTINCT s.notebook_id) = 1 THEN min(s.notebook_id) END
                 FROM notebook_sources s
                 WHERE s.document_id = d.id AND s.user_id = d.user_id), 0)::bigint AS notebook_id
FROM documents d
WHERE d.id = sqlc.arg(document_id);

-- name: ClaimUpdateDevices :many
-- The owner's phones to tell about an App Store version, each marked told
-- in the same statement, so a phone is told once per version however many
-- passes of the check overlap: the second finds the mark and claims
-- nothing. Only the ids the check chose (an iPhone on an older version) are
-- touched, and only the owner's.
UPDATE push_devices
SET update_notified_version = sqlc.arg(version)::text
WHERE user_id = sqlc.arg(user_id)
  AND id = ANY (sqlc.arg(device_ids)::bigint[])
  AND update_notified_version IS DISTINCT FROM sqlc.arg(version)::text
RETURNING id;

-- name: LatestAppUpdate :one
-- The account's one App Store update row, if it has kept one: each newer
-- version rewrites it rather than adding another, so the inbox never lists
-- versions that are already old.
SELECT id
FROM notifications
WHERE user_id = sqlc.arg(user_id) AND type = 'app_update'
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;

-- name: RewriteAppUpdate :exec
-- The update row, rewritten for the version it now announces, unread and
-- dated now: it is news again, and lists at the top of the inbox.
UPDATE notifications
SET title = sqlc.arg(title), message = sqlc.arg(message), metadata = sqlc.arg(metadata)::jsonb,
    read = false, created_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);
