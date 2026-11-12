-- The statements behind a public share link's files, the copy of a shared
-- chat, and the link to a Studio output.
--
-- The public statements name no caller: there is none. Each starts at the
-- share token instead, and reaches an artifact only when three things hold
-- together: the link has not expired, the link's own list (`artifact_ids`)
-- names the artifact, and the artifact belongs to the account that made the
-- link. The third is not in the Python and is what makes a wrong id in a
-- list harmless.
--
-- No statement selects more than its route sends or needs to find the bytes.

-- name: SharedPrimaryFile :one
-- The main file of an artifact the link shares.
-- owner: through the share token; the artifact must be the link owner's.
SELECT f.storage_backend, f.storage_key, f.original_filename, f.mime_type, f.checksum_sha256
FROM public_chat_snapshots s
JOIN artifacts a ON a.user_id = s.user_id
JOIN artifact_files f ON f.artifact_id = a.id AND f.role = 'primary'
WHERE s.share_token = sqlc.arg(share_token)
  AND (s.expires_at IS NULL OR s.expires_at > sqlc.arg(now)::timestamptz)
  AND a.id = sqlc.arg(artifact_id)::bigint
  AND jsonb_typeof(s.snapshot_data -> 'artifact_ids') = 'array'
  AND s.snapshot_data -> 'artifact_ids' @> to_jsonb(sqlc.arg(artifact_id)::bigint);

-- name: SharedFile :one
-- One file, by its id, of an artifact the link shares: a deck's PDF preview
-- as well as its main file.
-- owner: through the share token; the artifact must be the link owner's.
SELECT f.storage_backend, f.storage_key, f.original_filename, f.mime_type, f.checksum_sha256
FROM public_chat_snapshots s
JOIN artifacts a ON a.user_id = s.user_id
JOIN artifact_files f ON f.artifact_id = a.id
WHERE s.share_token = sqlc.arg(share_token)
  AND (s.expires_at IS NULL OR s.expires_at > sqlc.arg(now)::timestamptz)
  AND a.id = sqlc.arg(artifact_id)::bigint
  AND f.id = sqlc.arg(file_id)::bigint
  AND jsonb_typeof(s.snapshot_data -> 'artifact_ids') = 'array'
  AND s.snapshot_data -> 'artifact_ids' @> to_jsonb(sqlc.arg(artifact_id)::bigint);

-- name: SharedVideoSlides :one
-- The slides of a video artifact the link shares, for one slide's audio.
-- Only that key of the metadata is read; the scene code stays in the row.
-- owner: through the share token; the artifact must be the link owner's.
SELECT (a.metadata -> 'slides')::jsonb AS slides
FROM public_chat_snapshots s
JOIN artifacts a ON a.user_id = s.user_id
WHERE s.share_token = sqlc.arg(share_token)
  AND (s.expires_at IS NULL OR s.expires_at > sqlc.arg(now)::timestamptz)
  AND a.id = sqlc.arg(artifact_id)::bigint
  AND a.format = 'video'
  AND jsonb_typeof(s.snapshot_data -> 'artifact_ids') = 'array'
  AND s.snapshot_data -> 'artifact_ids' @> to_jsonb(sqlc.arg(artifact_id)::bigint);

-- name: FrozenMedia :one
-- The podcasts and the presentations frozen into a live link, and nothing
-- else of its copy: the messages can be megabytes and no route here reads
-- them.
-- owner: through the share token.
SELECT (snapshot_data -> 'podcasts')::jsonb AS podcasts,
       (snapshot_data -> 'video_presentations')::jsonb AS presentations
FROM public_chat_snapshots
WHERE share_token = sqlc.arg(share_token)
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now)::timestamptz);

-- name: LockAccountCopies :exec
-- One copy at a time for an account, for as long as the transaction lasts:
-- two first copies would otherwise each find no "Earlier chats" notebook
-- and each make one.
SELECT pg_advisory_xact_lock(hashtextextended('publicshare.clone:' || sqlc.arg(user_id)::text, 0));

-- name: LinkToCopy :one
-- The live link a copy is made from. `thread_id` is NULL for a link to a
-- Studio output, which cannot be copied.
-- owner: through the share token; whoever holds it may copy.
SELECT id, thread_id,
       left(COALESCE(NULLIF(snapshot_data ->> 'title', ''), 'Copied chat'), 500)::text AS title
FROM public_chat_snapshots
WHERE share_token = sqlc.arg(share_token)
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now)::timestamptz);

-- name: EarlierChatsNotebook :one
-- The caller's notebook for chats that began outside one. It is found by
-- the chats filed in it and not by its title, which its owner may change.
SELECT n.id
FROM notebooks n
JOIN notebook_earlier_chats e ON e.notebook_id = n.id AND e.user_id = n.user_id
WHERE n.user_id = sqlc.arg(user_id)
ORDER BY n.id
LIMIT 1;

-- name: CreateEarlierChatsNotebook :one
INSERT INTO notebooks (user_id, title, icon)
VALUES (sqlc.arg(user_id), sqlc.arg(title), sqlc.arg(icon))
RETURNING id;

-- name: InsertCopiedThread :one
-- The copy: the caller's own thread, in the caller's own notebook (the
-- (notebook_id, user_id) foreign key refuses any other), recording which
-- link it came from.
INSERT INTO new_chat_threads (user_id, title, notebook_id, cloned_from_thread_id, cloned_from_snapshot_id,
                              cloned_at, created_at, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(title), sqlc.arg(notebook_id), sqlc.narg(source_thread_id), sqlc.arg(snapshot_id),
        sqlc.arg(made_at)::timestamptz, sqlc.arg(made_at)::timestamptz, sqlc.arg(made_at)::timestamptz)
RETURNING id;

-- name: FileEarlierChat :exec
INSERT INTO notebook_earlier_chats (thread_id, notebook_id, user_id)
VALUES (sqlc.arg(thread_id), sqlc.arg(notebook_id), sqlc.arg(user_id));

-- name: CopyMessages :execrows
-- Every message of the frozen copy into the new thread, in one statement.
-- The copy holds only what the public page shows (text and the public
-- cards), so that is all a copy can carry. Each message is stamped a
-- microsecond after the one before it, because messages are read back in
-- (created_at, id) order and one shared instant would leave the order to
-- the ids alone. No author is written: the people who wrote the original
-- are not the caller, and their ids are not in the copy.
INSERT INTO new_chat_messages (thread_id, role, content, created_at)
SELECT sqlc.arg(thread_id)::bigint,
       upper(m.entry ->> 'role')::newchatmessagerole,
       CASE WHEN jsonb_typeof(m.entry -> 'content') = 'array' THEN m.entry -> 'content' ELSE '[]'::jsonb END,
       sqlc.arg(made_at)::timestamptz + m.position * interval '1 microsecond'
FROM public_chat_snapshots s
CROSS JOIN LATERAL jsonb_array_elements(
    CASE WHEN jsonb_typeof(s.snapshot_data -> 'messages') = 'array'
         THEN s.snapshot_data -> 'messages' ELSE '[]'::jsonb END
) WITH ORDINALITY AS m(entry, position)
WHERE s.id = sqlc.arg(snapshot_id)
  AND upper(m.entry ->> 'role') IN ('USER', 'ASSISTANT', 'SYSTEM')
ORDER BY m.position;

-- name: OutputToShare :one
-- The caller's output with the artifact its finished run made, locked until
-- its link is written so that two shares of it at once make one link. The
-- run and the artifact are joined only when they are the caller's and the
-- run is ready, so an unfinished output answers with no artifact.
SELECT o.id, o.title, o.kind,
       a.id AS artifact_id, a.format AS artifact_format, a.generation AS artifact_generation,
       COALESCE(NULLIF(d.source_markdown, ''), d.content, '')::text AS markdown
FROM notebook_outputs o
LEFT JOIN podcast_runs p ON p.id = o.podcast_id AND p.user_id = o.user_id AND p.status = 'ready'
LEFT JOIN deliverable_jobs j ON j.id = o.deliverable_job_id AND j.user_id = o.user_id AND j.status = 'ready'
LEFT JOIN artifacts a ON a.id = COALESCE(p.artifact_id, j.artifact_id) AND a.user_id = o.user_id
LEFT JOIN documents d ON d.id = a.document_id AND d.user_id = a.user_id
WHERE o.id = sqlc.arg(output_id) AND o.notebook_id = sqlc.arg(notebook_id) AND o.user_id = sqlc.arg(user_id)
FOR UPDATE OF o;

-- name: PrimaryFileSummary :one
-- What the link's copy says about the output's main file.
SELECT f.original_filename, f.mime_type, f.size_bytes
FROM artifact_files f
JOIN artifacts a ON a.id = f.artifact_id
WHERE f.artifact_id = sqlc.arg(artifact_id) AND a.user_id = sqlc.arg(user_id) AND f.role = 'primary';

-- name: DropExpiredOutputLinks :exec
-- An expired link stays dead: the same content shared again gets a new
-- address, so the old row goes first.
DELETE FROM public_chat_snapshots
WHERE notebook_output_id = sqlc.arg(output_id) AND user_id = sqlc.arg(user_id)
  AND content_hash = sqlc.arg(content_hash)
  AND expires_at IS NOT NULL AND expires_at <= sqlc.arg(now)::timestamptz;

-- name: RenewOutputLink :one
-- The same output, unchanged, shared again: the same address, with the
-- expiry just chosen and the title it has now (a rename shows on the link).
UPDATE public_chat_snapshots link
SET expires_at = sqlc.narg(expires_at),
    snapshot_data = jsonb_set(
        jsonb_set(snapshot_data, '{title}', to_jsonb(sqlc.arg(title)::text)),
        '{output,title}', to_jsonb(sqlc.arg(title)::text))
WHERE link.id = (
    SELECT same.id FROM public_chat_snapshots same
    WHERE same.notebook_output_id = sqlc.arg(output_id) AND same.user_id = sqlc.arg(user_id)
      AND same.content_hash = sqlc.arg(content_hash)
    ORDER BY same.id LIMIT 1)
RETURNING link.id, link.share_token, link.expires_at;

-- name: InsertOutputLink :one
INSERT INTO public_chat_snapshots (user_id, notebook_output_id, share_token, content_hash,
                                   snapshot_data, message_ids, expires_at)
VALUES (sqlc.arg(user_id), sqlc.arg(output_id), sqlc.arg(share_token), sqlc.arg(content_hash),
        sqlc.arg(snapshot_data), '{}', sqlc.narg(expires_at))
RETURNING id, share_token, expires_at;
