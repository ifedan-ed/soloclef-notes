-- The statements the three public share-link routes issue. Each is read
-- against the SQL SQLAlchemy compiled for the Python route, recorded under
-- `statements` in testdata/publicchat_python.json: the same table, the same
-- predicates, and nothing added -- no ORDER BY and no LIMIT, because the
-- Python statements carry neither.
--
-- The Python statements select every column of the mapped class. These name
-- only the columns a route reads, and here that is more than a saving: every
-- one of these routes answers a caller with no credential, so the columns a
-- statement can see are the outer bound of what the route can ever disclose.
-- A storage key that is never selected cannot be sent by a later edit.
--
-- None of these statements names `user_id`, and that is the design, not an
-- omission: there is no caller to compare it with. The share token is the
-- whole authorisation. The first statement finds the one snapshot a token
-- names, and every other statement is reached only with an artifact id the
-- route has already found in that snapshot's own allowlist. Each says so in
-- an `owner:` line.

-- name: PublicSnapshotByToken :one
-- _snapshot_row: the one row a share token names. `share_token` carries a
-- unique index (ix_public_chat_snapshots_share_token), so the Python
-- `.first()` and this `:one` read the same row and no order is needed.
--
-- `thread_id` is not selected. Only the clone reads it, and the clone is
-- not served here.
--
-- owner: through the share token. Whoever holds the token may read.
SELECT notebook_output_id, expires_at, snapshot_data
FROM public_chat_snapshots
WHERE share_token = sqlc.arg(share_token);

-- name: SharedVideoArtifact :one
-- get_snapshot_video_artifact: the artifact by id, and only if it is a video.
-- owner: through the snapshot. Its `artifact_ids` is the allowlist, and the
-- route has checked the id against it before this runs.
--
-- The Python route issues two statements, the artifact and then
-- `selectinload(Artifact.document)`. This is one, with the document joined
-- LEFT so that an artifact whose document is gone still answers with a null
-- title, as `artifact.document.title if artifact.document else None` does.
-- `artifacts.document_id` is NOT NULL with a foreign key, so the right side
-- is in practice always there.
SELECT a.id, a.metadata, d.title AS document_title
FROM artifacts a
LEFT JOIN documents d ON d.id = a.document_id
WHERE a.id = sqlc.arg(id) AND a.format = 'video';

-- name: SharedArtifactGeneration :one
-- shared_output_files' first statement: does the shared artifact still
-- exist, and which generation is it on.
--
-- owner: through the snapshot, which names the artifact it shares.
SELECT generation FROM artifacts WHERE id = sqlc.arg(id);

-- name: SharedArtifactFiles :many
-- shared_output_files' second statement: every file of the shared artifact.
-- No ORDER BY, because the Python statement has none: the route sorts the
-- rows itself, main file first and then by id, which is a total order.
--
-- `storage_backend`, `storage_key` and `checksum_sha256` are deliberately not
-- selected; the manifest a stranger is sent has no use for them.
--
-- owner: through the snapshot, which names the artifact it shares.
SELECT id, role::text AS role, original_filename, mime_type, size_bytes
FROM artifact_files
WHERE artifact_id = sqlc.arg(artifact_id);

-- name: SharedArtifactFormats :many
-- _upcast_legacy_public_artifact_formats: the format of each artifact a
-- legacy `save_artifact` result names without one. One statement with an
-- array where the Python route writes `IN (...)` over the same ids, as
-- the route would; the rows are read back by id, so their order is not read.
-- The array is bigint because an id is.
--
-- owner: through the snapshot, whose own messages name these artifacts.
SELECT id, format FROM artifacts WHERE id = ANY(sqlc.arg(ids)::bigint[]);
