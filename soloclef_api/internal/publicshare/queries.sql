-- The statements behind a Studio output's public link: the page and the
-- files a stranger holding the token reads, and the owner's create.
--
-- The public statements name no caller: there is none. Each starts at the
-- share token instead, and reaches an artifact only when three things hold
-- together: the link has not expired, the link's own list (`artifact_ids`)
-- names the artifact, and the artifact belongs to the account that made the
-- link. The third is what makes a wrong id in a list harmless.
--
-- No statement selects more than its route sends or needs to find the bytes.

-- name: PageLink :one
-- The link a page shows. The expiry is read rather than filtered on, so
-- the page can tell an expired link (410) from one that is gone (404).
-- `user_id` and `notebook_output_id` are not selected: the page never sends
-- them, and a column that is not read cannot be sent by a later edit.
-- owner: through the share token. Whoever holds it may read.
SELECT expires_at, created_at, snapshot_data
FROM output_public_links
WHERE share_token = sqlc.arg(share_token);

-- name: SharedArtifactFiles :one
-- The shared artifact's generation and every one of its files, main file
-- first and then by id, in one round trip. Its files are read as they are
-- now and not as they were when the link was made, so an output made again
-- after it was shared shows its new files. No storage key and no checksum:
-- the page has no use for them.
-- owner: through the share token; the artifact must be the link owner's.
SELECT a.generation,
       COALESCE((
           SELECT jsonb_agg(jsonb_build_object(
                      'file_id', f.id, 'role', f.role, 'filename', f.original_filename,
                      'mime_type', f.mime_type, 'size_bytes', f.size_bytes)
                  ORDER BY f.role = 'primary' DESC, f.id)
           FROM artifact_files f
           WHERE f.artifact_id = a.id
       ), '[]'::jsonb)::jsonb AS files
FROM output_public_links l
JOIN artifacts a ON a.user_id = l.user_id
WHERE l.share_token = sqlc.arg(share_token)
  AND (l.expires_at IS NULL OR l.expires_at > sqlc.arg(now)::timestamptz)
  AND a.id = sqlc.arg(artifact_id)::bigint
  AND jsonb_typeof(l.snapshot_data -> 'artifact_ids') = 'array'
  AND l.snapshot_data -> 'artifact_ids' @> to_jsonb(sqlc.arg(artifact_id)::bigint);

-- name: SharedPrimaryFile :one
-- The main file of an artifact the link shares, with the shared output's
-- title, which the file is named after as the owner's download is.
-- owner: through the share token; the artifact must be the link owner's.
SELECT f.storage_backend, f.storage_key, f.original_filename, f.mime_type, f.checksum_sha256,
       COALESCE(l.snapshot_data ->> 'title', '')::text AS title
FROM output_public_links l
JOIN artifacts a ON a.user_id = l.user_id
JOIN artifact_files f ON f.artifact_id = a.id AND f.role = 'primary'
WHERE l.share_token = sqlc.arg(share_token)
  AND (l.expires_at IS NULL OR l.expires_at > sqlc.arg(now)::timestamptz)
  AND a.id = sqlc.arg(artifact_id)::bigint
  AND jsonb_typeof(l.snapshot_data -> 'artifact_ids') = 'array'
  AND l.snapshot_data -> 'artifact_ids' @> to_jsonb(sqlc.arg(artifact_id)::bigint);

-- name: SharedFile :one
-- One file, by its id, of an artifact the link shares: a deck's PDF preview
-- as well as its main file.
-- owner: through the share token; the artifact must be the link owner's.
SELECT f.storage_backend, f.storage_key, f.original_filename, f.mime_type, f.checksum_sha256,
       COALESCE(l.snapshot_data ->> 'title', '')::text AS title
FROM output_public_links l
JOIN artifacts a ON a.user_id = l.user_id
JOIN artifact_files f ON f.artifact_id = a.id
WHERE l.share_token = sqlc.arg(share_token)
  AND (l.expires_at IS NULL OR l.expires_at > sqlc.arg(now)::timestamptz)
  AND a.id = sqlc.arg(artifact_id)::bigint
  AND f.id = sqlc.arg(file_id)::bigint
  AND jsonb_typeof(l.snapshot_data -> 'artifact_ids') = 'array'
  AND l.snapshot_data -> 'artifact_ids' @> to_jsonb(sqlc.arg(artifact_id)::bigint);

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
-- Or the artifact a notebook's chat made, which has no run to be ready.
LEFT JOIN artifacts a ON a.id = COALESCE(p.artifact_id, j.artifact_id, o.artifact_id) AND a.user_id = o.user_id
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
DELETE FROM output_public_links
WHERE notebook_output_id = sqlc.arg(output_id) AND user_id = sqlc.arg(user_id)
  AND content_hash = sqlc.arg(content_hash)
  AND expires_at IS NOT NULL AND expires_at <= sqlc.arg(now)::timestamptz;

-- name: RenewOutputLink :one
-- The same output, unchanged, shared again: the same address, with the
-- expiry just chosen and the title it has now (a rename shows on the link).
UPDATE output_public_links link
SET expires_at = sqlc.narg(expires_at),
    snapshot_data = jsonb_set(
        jsonb_set(snapshot_data, '{title}', to_jsonb(sqlc.arg(title)::text)),
        '{output,title}', to_jsonb(sqlc.arg(title)::text))
WHERE link.id = (
    SELECT same.id FROM output_public_links same
    WHERE same.notebook_output_id = sqlc.arg(output_id) AND same.user_id = sqlc.arg(user_id)
      AND same.content_hash = sqlc.arg(content_hash)
    ORDER BY same.id LIMIT 1)
RETURNING link.id, link.share_token, link.expires_at;

-- name: InsertOutputLink :one
INSERT INTO output_public_links (user_id, notebook_output_id, share_token, content_hash,
                                 snapshot_data, expires_at)
VALUES (sqlc.arg(user_id), sqlc.arg(output_id), sqlc.arg(share_token), sqlc.arg(content_hash),
        sqlc.arg(snapshot_data), sqlc.narg(expires_at))
RETURNING id, share_token, expires_at;
