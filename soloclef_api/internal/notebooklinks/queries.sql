-- The statements of a notebook's live public link.
--
-- Two kinds of caller reach them. The owner's statements name `user_id`,
-- which is the whole of the access check. A visitor's statements start from
-- a link already resolved by its token (LiveLink) and are handed that
-- link's notebook and owner: every one names both, so nothing outside the
-- shared notebook can be reached by guessing an id.

-- name: LinkOfNotebook :one
-- One of the caller's notebooks with its link, if it has one. No row means
-- the notebook is not the caller's; a row with no link id means it is not
-- shared.
SELECT n.id AS notebook_id, n.title, n.icon,
       l.id AS link_id, l.share_token, l.created_at, l.expires_at
FROM notebooks n
LEFT JOIN notebook_public_links l ON l.notebook_id = n.id AND l.user_id = n.user_id
WHERE n.id = sqlc.arg(notebook_id) AND n.user_id = sqlc.arg(user_id);

-- name: LinksOfOwner :many
-- Every live link of the caller, newest first; `id` makes the order total.
SELECT n.id AS notebook_id, n.title, n.icon,
       l.id AS link_id, l.share_token, l.created_at, l.expires_at
FROM notebook_public_links l
JOIN notebooks n ON n.id = l.notebook_id AND n.user_id = l.user_id
WHERE l.user_id = sqlc.arg(user_id)
ORDER BY l.created_at DESC, l.id DESC;

-- name: ShareNotebook :execrows
-- The notebook's one link: made, or kept with a new expiry, or given a new
-- address when asked to or when the old one had expired (an address that
-- stopped working must not start working again). One statement, so two
-- presses of Share cannot race into the unique constraint. The SELECT is
-- what confines it to the caller's notebook: no row, nothing written.
INSERT INTO notebook_public_links (notebook_id, user_id, share_token, expires_at, created_at)
SELECT n.id, n.user_id, sqlc.arg(new_token), sqlc.narg(expires_at), sqlc.arg(now)::timestamptz
FROM notebooks n
WHERE n.id = sqlc.arg(notebook_id) AND n.user_id = sqlc.arg(user_id)
ON CONFLICT (notebook_id) DO UPDATE SET
    expires_at  = EXCLUDED.expires_at,
    share_token = CASE WHEN sqlc.arg(regenerate)::boolean
                         OR notebook_public_links.expires_at <= sqlc.arg(now)::timestamptz
                       THEN EXCLUDED.share_token ELSE notebook_public_links.share_token END,
    -- A new address is a new link, and is dated as one.
    created_at  = CASE WHEN sqlc.arg(regenerate)::boolean
                         OR notebook_public_links.expires_at <= sqlc.arg(now)::timestamptz
                       THEN EXCLUDED.created_at ELSE notebook_public_links.created_at END;

-- name: StopSharing :execrows
DELETE FROM notebook_public_links
WHERE notebook_id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- name: RevokeLink :execrows
DELETE FROM notebook_public_links
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: LiveLink :one
-- A link by its token, with the notebook it shows. The token is the only
-- credential a visitor has. An account that has been deactivated (deleted
-- accounts are, before they are erased) shares nothing any more.
SELECT l.notebook_id, l.user_id, l.share_token, l.expires_at, n.title, n.icon
FROM notebook_public_links l
JOIN notebooks n ON n.id = l.notebook_id AND n.user_id = l.user_id
JOIN "user" u ON u.id = l.user_id AND u.is_active
WHERE l.share_token = sqlc.arg(share_token);

-- name: SharedSources :many
-- The documents a visitor sees in the source list: the notebook's document
-- sources in the order they were added, then the documents directly inside
-- its folder sources (the rule the notebook's chat reads by). Never a
-- studio artifact, never a document being deleted. `only_id` narrows it to
-- one document (0 is all of them), so one source is checked against exactly
-- the list it would appear in.
SELECT d.id, d.title, d.document_type::text AS document_type,
       EXISTS (SELECT 1 FROM document_files f
               WHERE f.document_id = d.id AND f.user_id = d.user_id AND f.kind = 'ORIGINAL') AS has_file
FROM documents d
LEFT JOIN notebook_sources direct
       ON direct.document_id = d.id AND direct.notebook_id = sqlc.arg(notebook_id) AND direct.user_id = d.user_id
WHERE d.user_id = sqlc.arg(user_id)
  AND d.document_type <> 'ARTIFACT'
  AND d.status ->> 'state' <> 'deleting'
  AND (direct.id IS NOT NULL
       OR d.folder_id IN (SELECT s.folder_id FROM notebook_sources s
                          WHERE s.notebook_id = sqlc.arg(notebook_id) AND s.user_id = sqlc.arg(user_id)
                            AND s.folder_id IS NOT NULL))
  AND (sqlc.arg(only_id)::bigint = 0 OR d.id = sqlc.arg(only_id)::bigint)
ORDER BY direct.added_at NULLS LAST, direct.id NULLS LAST, d.id;

-- name: SourceText :one
-- One document's text and its newest original file. It is only ever asked
-- about a document SharedSources has just answered, and names the owner all
-- the same. File id 0, which no file has, is "no original".
SELECT COALESCE(d.source_markdown, d.content)::text AS markdown,
       COALESCE(f.id, 0)::bigint AS file_id, COALESCE(f.original_filename, '')::text AS original_filename,
       COALESCE(f.mime_type, '')::text AS mime_type, COALESCE(f.size_bytes, 0)::bigint AS size_bytes
FROM documents d
LEFT JOIN LATERAL (
    SELECT o.id, o.original_filename, o.mime_type, o.size_bytes
    FROM document_files o
    WHERE o.document_id = d.id AND o.user_id = d.user_id AND o.kind = 'ORIGINAL'
    ORDER BY o.created_at DESC, o.id DESC
    LIMIT 1
) f ON true
WHERE d.id = sqlc.arg(document_id) AND d.user_id = sqlc.arg(user_id);

-- name: SourceFile :one
-- Where one original file's bytes are. Derived copies (redacted, filled)
-- stay private: only an original is ever sent to a visitor.
SELECT storage_backend, storage_key, original_filename, mime_type, checksum_sha256
FROM document_files
WHERE id = sqlc.arg(file_id) AND document_id = sqlc.arg(document_id)
  AND user_id = sqlc.arg(user_id) AND kind = 'ORIGINAL';

-- name: SharedOutputs :many
-- The notebook's finished studio outputs, newest first. The joins to the
-- runs are what "finished" means: a queued, running or failed run joins
-- nothing, so its output has no artifact and is not listed. An artifact
-- whose document is being deleted is already gone to a visitor.
SELECT o.id, o.kind, o.title, o.created_at,
       a.id AS artifact_id, a.format, a.metadata, p.duration_seconds AS podcast_duration
FROM notebook_outputs o
LEFT JOIN podcast_runs p ON p.id = o.podcast_id AND p.user_id = o.user_id AND p.status = 'ready'
LEFT JOIN deliverable_jobs j ON j.id = o.deliverable_job_id AND j.user_id = o.user_id AND j.status = 'ready'
JOIN artifacts a ON a.id = COALESCE(p.artifact_id, j.artifact_id) AND a.user_id = o.user_id
JOIN documents d ON d.id = a.document_id AND d.user_id = a.user_id AND d.status ->> 'state' <> 'deleting'
WHERE o.notebook_id = sqlc.arg(notebook_id) AND o.user_id = sqlc.arg(user_id)
ORDER BY o.created_at DESC, o.id DESC;

-- name: SharedChat :many
-- The messages of the chat the notebook's owner sees in it: the newest
-- unarchived one, the studio's own threads aside.
SELECT m.role::text AS role, m.content, m.created_at
FROM new_chat_messages m
WHERE m.thread_id = (
    SELECT t.id FROM new_chat_threads t
    WHERE t.notebook_id = sqlc.arg(notebook_id) AND t.user_id = sqlc.arg(user_id)
      AND NOT t.archived AND t.source <> 'artifact_api'
    ORDER BY t.updated_at DESC, t.id DESC
    LIMIT 1)
ORDER BY m.created_at, m.id;

-- name: CitedPassages :many
-- Which cited passages are in the documents a visitor may see, with the
-- document and page each is on. The ids come out of message text, so the
-- owner and the document list are what make them safe to ask about.
SELECT id, document_id, page_start
FROM chunks
WHERE id = ANY(sqlc.arg(chunk_ids)::bigint[]) AND user_id = sqlc.arg(user_id)
  AND document_id = ANY(sqlc.arg(document_ids)::bigint[]);

-- name: MadeArtifacts :many
-- What the chat's cards resolve to: each artifact named outright, each
-- finished podcast's and each finished job's, as (kind, id named, artifact),
-- with the artifact's title and format. Only the owner's artifacts, and
-- none whose document is being deleted. The ids come out of message
-- content, which is why every branch names the owner.
SELECT made.kind::text AS kind, made.ref::bigint AS ref, a.id AS artifact_id, d.title, a.format
FROM (
    SELECT 'artifact' AS kind, x.id AS ref, x.id AS artifact_id
    FROM artifacts x
    WHERE x.id = ANY(sqlc.arg(artifact_ids)::bigint[]) AND x.user_id = sqlc.arg(user_id)
    UNION ALL
    SELECT 'podcast', p.id, p.artifact_id
    FROM podcast_runs p
    WHERE p.id = ANY(sqlc.arg(podcast_ids)::bigint[]) AND p.user_id = sqlc.arg(user_id)
      AND p.status = 'ready' AND p.artifact_id IS NOT NULL
    UNION ALL
    SELECT 'job', j.id, j.artifact_id
    FROM deliverable_jobs j
    WHERE j.id = ANY(sqlc.arg(job_ids)::bigint[]) AND j.user_id = sqlc.arg(user_id)
      AND j.status = 'ready' AND j.artifact_id IS NOT NULL
) made
JOIN artifacts a ON a.id = made.artifact_id AND a.user_id = sqlc.arg(user_id)
JOIN documents d ON d.id = a.document_id AND d.user_id = a.user_id AND d.status ->> 'state' <> 'deleting'
ORDER BY made.kind, made.ref;

-- name: IsOutputArtifact :one
-- Whether the artifact is one of the notebook's finished outputs (the rule
-- SharedOutputs lists by).
SELECT EXISTS (
    SELECT 1
    FROM notebook_outputs o
    LEFT JOIN podcast_runs p ON p.id = o.podcast_id AND p.user_id = o.user_id AND p.status = 'ready'
    LEFT JOIN deliverable_jobs j ON j.id = o.deliverable_job_id AND j.user_id = o.user_id AND j.status = 'ready'
    WHERE o.notebook_id = sqlc.arg(notebook_id) AND o.user_id = sqlc.arg(user_id)
      AND COALESCE(p.artifact_id, j.artifact_id) = sqlc.arg(artifact_id)::bigint);

-- name: ArtifactShown :one
-- An artifact as the public viewers read it. Asked only after the artifact
-- was found to be one the link shows.
SELECT a.id, a.format, a.generation, a.updated_at, d.title,
       COALESCE(d.source_markdown, d.content)::text AS markdown
FROM artifacts a
JOIN documents d ON d.id = a.document_id AND d.user_id = a.user_id
WHERE a.id = sqlc.arg(artifact_id) AND a.user_id = sqlc.arg(user_id)
  AND d.status ->> 'state' <> 'deleting';

-- name: ArtifactFiles :many
-- The artifact's files, the primary first. The join names the owner.
SELECT f.id, f.role::text AS role, f.storage_backend, f.storage_key, f.original_filename,
       f.mime_type, f.size_bytes, f.checksum_sha256
FROM artifact_files f
JOIN artifacts a ON a.id = f.artifact_id AND a.user_id = sqlc.arg(user_id)
WHERE f.artifact_id = sqlc.arg(artifact_id)
ORDER BY (f.role <> 'primary'), f.id;
