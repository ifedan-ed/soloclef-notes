-- The statements of the artifact routes. Each names only the columns its
-- route reads, and every one that reads or writes an artifact names its
-- owner: a row is the caller's or it is not found, and there is no other
-- access check anywhere.

-- name: ArtifactsOfOwner :many
-- The list without a thread filter. The order is total -- `id` is the
-- primary key -- so no plan can reorder two rows that tie on `updated_at`.
SELECT a.id, a.document_id, d.title, a.format, a.generation, d.status,
       a.thread_id, a.created_at, a.updated_at, a.metadata
FROM artifacts a
JOIN documents d ON a.document_id = d.id
WHERE a.user_id = sqlc.arg(user_id)
ORDER BY a.updated_at DESC, a.id DESC;

-- name: ArtifactsOfOwnerInThread :many
-- The list with `?thread_id=`. A second statement rather than one with an
-- optional predicate, so that each has a plan of its own.
SELECT a.id, a.document_id, d.title, a.format, a.generation, d.status,
       a.thread_id, a.created_at, a.updated_at, a.metadata
FROM artifacts a
JOIN documents d ON a.document_id = d.id
WHERE a.user_id = sqlc.arg(user_id) AND a.thread_id = sqlc.arg(thread_id)
ORDER BY a.updated_at DESC, a.id DESC;

-- name: VideoArtifactOfOwner :one
-- One artifact, as the video route reads it. Another account's artifact is
-- not found.
SELECT a.id, d.title, a.format, a.metadata, a.thread_id
FROM artifacts a
JOIN documents d ON a.document_id = d.id
WHERE a.id = sqlc.arg(id) AND a.user_id = sqlc.arg(user_id);

-- name: DocumentOfArtifactOfOwner :one
-- The delete's read: which document the artifact stands on, and that
-- document's status as it is now -- which decides whether it may be deleted
-- and is what the marker is compared against.
SELECT d.id, d.status
FROM artifacts a
JOIN documents d ON a.document_id = d.id
WHERE a.id = sqlc.arg(id) AND a.user_id = sqlc.arg(user_id);

-- name: MarkArtifactDocumentDeleting :execrows
-- The marker, written only over the status the decision was made on. A
-- document that started indexing, or was marked by another delete, between
-- the read and this statement matches no row, and the caller is told to try
-- again rather than the document being removed under whatever claimed it.
--
-- The owner is named although the id was just read through the owner's
-- artifact: no statement writes an owned table on an id alone.
UPDATE documents SET status = '{"state": "deleting"}'::jsonb
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status IS NOT DISTINCT FROM sqlc.arg(was)::jsonb;

-- ---------------------------------------------------------------------------
-- The ten routes that read an artifact's bytes: the manifest, the three that
-- stream, and the six study-state writes. Each statement still names only the
-- columns its route reads -- which matters more here than above, because
-- `artifacts.metadata` of a video holds every slide and scene, and
-- `documents.content` is a whole document. A route that wants one key of the
-- first or none of the second asks Postgres for exactly that.

-- name: ManifestOfArtifact :one
-- Everything the manifest says about the artifact itself. The three jsonb
-- columns are *paths into* metadata, not metadata: the legacy reference, and
-- this caller's own study state for each of the two formats that have one.
-- `->` on something that is not an object is NULL rather than an error, so a
-- metadata column holding a list or a string reads as "no state", which is
-- what it is.
--
-- The state is keyed by the row's own `user_id`, not by a second parameter:
-- the caller is the owner or the row is not found, and one parameter cannot
-- be both the uuid of the predicate and the text of a jsonb key.
--
-- `NULLIF(.., '')` because an empty source_markdown means "none was kept",
-- and the document's content is then what the artifact reads as.
SELECT a.id, a.document_id, d.title, a.format, a.generation, a.updated_at,
       COALESCE(NULLIF(d.source_markdown, ''), d.content)::text AS markdown,
       (a.metadata -> 'legacy')::jsonb AS legacy,
       (a.metadata -> 'flashcards' -> 'study_by_user' -> a.user_id::text)::jsonb AS flashcard_state,
       (a.metadata -> 'quiz' -> 'progress_by_user' -> a.user_id::text)::jsonb AS quiz_state
FROM artifacts a
JOIN documents d ON a.document_id = d.id
WHERE a.id = sqlc.arg(id) AND a.user_id = sqlc.arg(user_id);

-- name: FilesOfArtifact :many
-- An artifact's files, the primary first and then by id. The order is total:
-- (artifact_id, role) is unique, so there is at most one primary, and id is
-- the primary key.
--
-- owner: through artifacts. `artifact_files` has no owner column, so the
-- join is what keeps this from listing another account's files whatever id
-- it is handed.
SELECT f.id, f.role::text AS role, f.original_filename, f.mime_type, f.size_bytes,
       f.storage_backend, f.storage_key, f.checksum_sha256
FROM artifact_files f
JOIN artifacts a ON f.artifact_id = a.id
WHERE f.artifact_id = sqlc.arg(artifact_id) AND a.user_id = sqlc.arg(user_id)
ORDER BY (f.role <> 'primary'), f.id;

-- name: DownloadOfArtifact :one
-- The download: the artifact's primary file when it has one, and the
-- document's Markdown when it has none. The CASE is what keeps a whole
-- document from being read out of the table and thrown away for every
-- artifact that *does* have a file, which is nearly all of them; for those
-- the column is the empty string and `file_id` says why.
SELECT d.title,
       f.id AS file_id, f.original_filename, f.mime_type,
       f.storage_backend, f.storage_key, f.checksum_sha256,
       COALESCE(CASE WHEN f.id IS NULL
                     THEN COALESCE(NULLIF(d.source_markdown, ''), d.content) END,
                '')::text AS markdown
FROM artifacts a
JOIN documents d ON a.document_id = d.id
LEFT JOIN artifact_files f ON f.artifact_id = a.id AND f.role = 'primary'
WHERE a.id = sqlc.arg(id) AND a.user_id = sqlc.arg(user_id);

-- name: FileOfArtifactOfOwner :one
-- One file, found only through its artifact and that artifact's owner: a
-- file id from another artifact, or an artifact of another account, is not
-- found.
SELECT f.id, f.role::text AS role, f.original_filename, f.mime_type, f.size_bytes,
       f.storage_backend, f.storage_key, f.checksum_sha256
FROM artifact_files f
JOIN artifacts a ON f.artifact_id = a.id
WHERE f.id = sqlc.arg(file_id) AND f.artifact_id = sqlc.arg(artifact_id)
  AND a.user_id = sqlc.arg(user_id);

-- name: SlidesOfArtifact :one
-- A video's slides, for the route that streams one slide's narration. Only
-- the `slides` key: the scene codes beside it are the bulk of the column and
-- this route has no use for them.
SELECT a.format, (a.metadata -> 'slides')::jsonb AS slides
FROM artifacts a
WHERE a.id = sqlc.arg(id) AND a.user_id = sqlc.arg(user_id);

-- name: StudySourceOfArtifact :one
-- What a study-state write reads before it locks anything: which format and
-- generation the artifact is, and where its primary file -- the deck or the
-- quiz -- is stored.
SELECT a.format, a.generation,
       f.id AS file_id, f.storage_backend, f.storage_key
FROM artifacts a
LEFT JOIN artifact_files f ON f.artifact_id = a.id AND f.role = 'primary'
WHERE a.id = sqlc.arg(id) AND a.user_id = sqlc.arg(user_id);

-- name: LockStudyStates :one
-- The lock a study-state write is made under. Two writes to one deck (two
-- tabs, a phone and a laptop) write the same row, and without the lock the
-- second would be computed from a state that did not include the first.
--
-- `section` is 'flashcards' or 'quiz' and `holder` the key under it that
-- maps a user's id to their state ('study_by_user', 'progress_by_user');
-- together they are the only part of metadata the write reads.
SELECT a.format, a.generation,
       (a.metadata -> sqlc.arg(section)::text -> sqlc.arg(holder)::text)::jsonb AS states
FROM artifacts a
WHERE a.id = sqlc.arg(id) AND a.user_id = sqlc.arg(user_id)
FOR UPDATE;

-- name: StoreStudyStates :exec
-- Writes one section of metadata and leaves every other key exactly as it
-- is, in the database: the row is never read into this tier and written
-- back, so nothing else stored there can be reformatted or lost on the way.
-- Metadata that is NULL, or is not an object, becomes an object holding the
-- section.
--
-- `updated_at` is deliberately not touched. It is the time the artifact's
-- *content* last changed and what the Studio's list is ordered by; flipping
-- a flashcard must not move a deck to the top of the list.
UPDATE artifacts
SET metadata = jsonb_set(
        CASE WHEN jsonb_typeof(metadata) = 'object' THEN metadata ELSE '{}'::jsonb END,
        ARRAY[sqlc.arg(section)::text],
        jsonb_build_object(sqlc.arg(holder)::text, sqlc.arg(states)::jsonb))
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: ClearStudyStates :exec
-- Removes a section that no longer holds anyone's state, rather than leaving
-- an empty object behind. `updated_at` is left alone for the reason above.
UPDATE artifacts
SET metadata = metadata - sqlc.arg(section)::text
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND jsonb_typeof(metadata) = 'object';
