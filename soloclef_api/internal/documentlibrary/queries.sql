-- The statements of the document library. Each names the owner; a document
-- being deleted (status "deleting") is already gone as far as a person can
-- tell, so every read and write passes over it.

-- name: DocumentPage :many
-- A page of the owner's documents with the count of everything the filters
-- match: one window count, so the page and its total come from one statement
-- and cannot disagree.
--
-- The order is total. None of the three sort columns is unique (a batch of
-- uploads shares a created_at, titles repeat), and an ORDER BY that ties
-- leaves the tied rows to the plan, so paging could show a document twice
-- and another not at all. The id settles every tie, in the direction of the
-- sort. `sort` "id" is the title search's order: every CASE is NULL and the
-- id alone decides.
--
-- The preview is cut in the database so that a page of long documents does
-- not send their whole text to this process to be thrown away.
SELECT d.id, d.title, d.document_type::text AS document_type, d.document_metadata,
       left(COALESCE(NULLIF(d.content, ''), d.source_markdown, ''), sqlc.arg(preview_chars)::int)::text AS preview,
       d.content_hash, d.unique_identifier_hash, d.created_at, d.updated_at, d.folder_id, d.status,
       count(*) OVER () AS total
FROM documents d
WHERE d.user_id = sqlc.arg(user_id)
  AND d.status ->> 'state' <> 'deleting'
  AND (cardinality(sqlc.arg(document_types)::text[]) = 0
       OR d.document_type::text = ANY(sqlc.arg(document_types)::text[]))
  AND (NOT sqlc.arg(unfiled)::boolean OR d.folder_id IS NULL)
  AND (sqlc.narg(folder_id)::bigint IS NULL OR d.folder_id = sqlc.narg(folder_id)::bigint)
  AND (sqlc.narg(title_pattern)::text IS NULL OR d.title ILIKE sqlc.narg(title_pattern)::text)
ORDER BY
  CASE WHEN sqlc.arg(sort)::text = 'title' AND NOT sqlc.arg(descending)::boolean THEN lower(d.title) END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'title' AND sqlc.arg(descending)::boolean THEN lower(d.title) END DESC,
  CASE WHEN sqlc.arg(sort)::text = 'document_type' AND NOT sqlc.arg(descending)::boolean THEN d.document_type::text END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'document_type' AND sqlc.arg(descending)::boolean THEN d.document_type::text END DESC,
  CASE WHEN sqlc.arg(sort)::text = 'created_at' AND NOT sqlc.arg(descending)::boolean THEN d.created_at END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'created_at' AND sqlc.arg(descending)::boolean THEN d.created_at END DESC,
  CASE WHEN NOT sqlc.arg(descending)::boolean THEN d.id END ASC,
  CASE WHEN sqlc.arg(descending)::boolean THEN d.id END DESC
LIMIT sqlc.arg(page_size)::bigint OFFSET sqlc.arg(skip)::bigint;

-- name: DocumentCount :one
-- The total for a page past the end, which has no row to carry the window
-- count. The filters are DocumentPage's, to the letter.
SELECT count(*)
FROM documents d
WHERE d.user_id = sqlc.arg(user_id)
  AND d.status ->> 'state' <> 'deleting'
  AND (cardinality(sqlc.arg(document_types)::text[]) = 0
       OR d.document_type::text = ANY(sqlc.arg(document_types)::text[]))
  AND (NOT sqlc.arg(unfiled)::boolean OR d.folder_id IS NULL)
  AND (sqlc.narg(folder_id)::bigint IS NULL OR d.folder_id = sqlc.narg(folder_id)::bigint)
  AND (sqlc.narg(title_pattern)::text IS NULL OR d.title ILIKE sqlc.narg(title_pattern)::text);

-- name: DocumentOfOwner :one
-- One document with its text: the Markdown a person reads and edits, which
-- is `source_markdown` where there is one and the indexed `content`
-- otherwise.
SELECT d.id, d.title, d.document_type::text AS document_type, d.document_metadata,
       COALESCE(d.source_markdown, d.content, '')::text AS body,
       d.content_hash, d.unique_identifier_hash, d.created_at, d.updated_at, d.folder_id, d.status
FROM documents d
WHERE d.id = sqlc.arg(id) AND d.user_id = sqlc.arg(user_id)
  AND d.status ->> 'state' <> 'deleting';

-- name: FolderIsOwned :one
SELECT EXISTS (SELECT 1 FROM folders WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id));

-- name: PlaceAddedNote :exec
-- Where an added note is filed and where its text came from. The note's
-- rows are written by the notes package's statements; these two columns are
-- what this route adds to them. The composite foreign key refuses another
-- account's folder as well; the route checks first so it can answer 404.
UPDATE documents
SET folder_id = sqlc.narg(folder_id)::bigint,
    document_metadata = CASE WHEN sqlc.narg(source_url)::text IS NULL THEN document_metadata
                             ELSE COALESCE(document_metadata, '{}'::jsonb)
                                  || jsonb_build_object('source_url', sqlc.narg(source_url)::text) END
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: DocumentForSave :one
-- The document as a save reads it, locked until the save commits so that two
-- saves cannot both keep the same "current" text as a version. A document
-- being deleted is read too, so the route can refuse it by name.
SELECT d.id, d.title, d.content_hash, d.source_markdown, d.document_type::text AS document_type,
       (d.status ->> 'state')::text AS state
FROM documents d
WHERE d.id = sqlc.arg(id) AND d.user_id = sqlc.arg(user_id)
FOR UPDATE;

-- name: MarkSaved :one
-- What a save owes beyond the text and title (which the version step
-- writes): the hash of the new text, the time, and "pending", because a job
-- is queued for it after the commit.
UPDATE documents
SET content_hash = sqlc.arg(content_hash),
    updated_at = sqlc.arg(now)::timestamptz,
    status = CASE WHEN sqlc.arg(text_changed)::boolean THEN '{"state": "pending"}'::jsonb ELSE status END
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING updated_at;
