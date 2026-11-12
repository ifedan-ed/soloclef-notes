-- The statements of the routes this package serves.
--
-- A document is its owner's or it does not exist: every statement on
-- `documents`, `chunks` or `folders` names `user_id`, so another account's
-- row is no row. `document_versions` has no owner column of its own; its
-- statements are reached only after the route has read the document with its
-- owner, and each says so.
--
-- A statement names only the columns its route reads.

-- name: CitedChunkByID :one
-- The chunk a citation names, if it is the caller's: the document it
-- belongs to, and the two columns that say where in that document it stands.
SELECT id, document_id, "position" FROM chunks
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)::uuid;

-- name: DocumentOfChunk :one
-- The document a cited chunk belongs to, with every column the answer is
-- built from. `document_type` is the enum's label.
SELECT id, title, document_type::text AS document_type, document_metadata,
       content, content_hash, unique_identifier_hash, created_at, updated_at
FROM documents WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)::uuid;

-- name: OwnsDocument :one
-- Whether this account has this document: what the chunk page and the two
-- version reads ask before they read anything of it. No row is the answer
-- for another account's document and for an id that names nothing alike.
SELECT true::boolean AS owned FROM documents
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)::uuid;

-- name: CountChunksOfDocument :one
-- How many chunks the caller's document has.
SELECT count(*) FROM chunks
WHERE document_id = sqlc.arg(document_id) AND user_id = sqlc.arg(user_id)::uuid;

-- name: CountChunksBefore :one
-- How many of the document's chunks come before the cited one in the order
-- the page is read in.
SELECT count(*) FROM chunks
WHERE document_id = sqlc.arg(document_id)
  AND user_id = sqlc.arg(user_id)::uuid
  AND ("position" < sqlc.arg(cited_position)
       OR "position" = sqlc.arg(cited_position) AND id < sqlc.arg(cited_id));

-- name: ChunksOfDocumentPage :many
-- A run of the document's chunks in reading order, which both chunk routes
-- issue. The order is total -- `id` is the primary key -- so no plan can
-- reorder two chunks that share a position.
--
-- Both bounds are bigint: an OFFSET past int32 is an empty page, not a
-- failure, and a page number of two thousand million is a request a client
-- can make.
SELECT created_at, id, content, document_id, page_start, page_end
FROM chunks
WHERE document_id = sqlc.arg(document_id)
  AND user_id = sqlc.arg(user_id)::uuid
ORDER BY "position", id
LIMIT sqlc.arg(page_limit)::bigint OFFSET sqlc.arg(page_offset)::bigint;

-- name: VersionsOfDocument :many
-- A document's history, newest first. `uq_document_versions_number` makes
-- (document_id, version_number) unique, so this order is total too.
-- owner: through documents (OwnsDocument is asked first).
SELECT version_number, title, content_hash, created_at
FROM document_versions
WHERE document_id = $1
ORDER BY version_number DESC;

-- name: VersionOfDocument :one
-- owner: through documents (OwnsDocument or DocumentForRestore is asked
-- first).
-- One version, which the single read and the restore both issue. The
-- restore copies the markdown and the title out of this row before it
-- snapshots, so it needs nothing that says which row it was: whatever the
-- snapshot then overwrites or deletes, what is restored is what was read here.
SELECT version_number, title, content_hash, source_markdown, created_at
FROM document_versions
WHERE document_id = $1 AND version_number = $2;

-- name: DocumentForRestore :one
-- The caller's document as the restore reads it: the three columns the
-- snapshot copies.
SELECT id, title, content_hash, source_markdown
FROM documents WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)::uuid;

-- name: HighestVersionNumberOfDocument :one
-- The first statement of the restore's snapshot. A restore always adds a
-- version and never overwrites the newest one (restore.go says why).
-- owner: through documents (DocumentForRestore, earlier in the request).
SELECT coalesce(max(version_number), 0)::bigint FROM document_versions WHERE document_id = $1;

-- name: InsertVersion :exec
-- The snapshot: always a new row. `created_at` is the request's own clock
-- and not the column's default, so the ninety-day cut-off below is measured
-- on the same clock.
-- owner: through documents (DocumentForRestore, earlier in the request).
INSERT INTO document_versions (document_id, version_number, source_markdown,
                               content_hash, title, created_at)
VALUES (sqlc.arg(document_id), sqlc.arg(version_number), sqlc.narg(source_markdown),
        sqlc.arg(content_hash), sqlc.narg(title), sqlc.arg(created_at));

-- name: DeleteVersionsOlderThan :exec
-- The ninety-day clean-up.
-- owner: through documents (DocumentForRestore, earlier in the request).
DELETE FROM document_versions WHERE document_id = $1 AND created_at < $2;

-- name: CountVersionsOfDocument :one
-- owner: through documents (DocumentForRestore, earlier in the request).
SELECT count(*) FROM document_versions WHERE document_id = $1;

-- name: OldestVersionIDs :many
-- The versions past the cap of twenty, oldest first.
-- owner: through documents (DocumentForRestore, earlier in the request).
SELECT id FROM document_versions
WHERE document_id = sqlc.arg(document_id)
ORDER BY version_number ASC
LIMIT sqlc.arg(excess)::bigint;

-- name: DeleteVersionsByID :exec
-- The versions OldestVersionIDs named. The document is repeated so that an
-- id list can never reach another document's versions.
-- owner: through documents (DocumentForRestore, earlier in the request).
DELETE FROM document_versions
WHERE document_id = sqlc.arg(document_id) AND id = ANY(sqlc.arg(ids)::bigint[]);

-- name: UpdateRestoredDocument :exec
-- The restore itself: the version's text and title, and the mark that sends
-- the document back through the indexer.
--
-- The last predicate keeps a restore that changes nothing from writing a
-- row: a version identical to the document, restored onto a document already
-- waiting to be reindexed, would otherwise be an UPDATE that Zero replicates
-- to every open client for no change.
UPDATE documents
SET source_markdown = sqlc.narg(source_markdown), title = sqlc.arg(title),
    content_needs_reindexing = true
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)::uuid
  AND (source_markdown IS DISTINCT FROM sqlc.narg(source_markdown)
       OR title IS DISTINCT FROM sqlc.arg(title)
       OR NOT content_needs_reindexing);

-- ---------------------------------------------------------------------------
-- The four reads about all of the caller's documents. Each ends on an ORDER
-- BY that is total, so two requests for one page read the same rows.

-- name: StatusOfDocuments :many
-- The caller's documents among the ids given, in id order: the status poll.
-- An id that is another account's is simply not among the rows.
SELECT id, title, document_type::text AS document_type, status
FROM documents
WHERE user_id = sqlc.arg(user_id)::uuid
  AND id = ANY(sqlc.arg(document_ids)::bigint[])
ORDER BY id;

-- name: TypeCountsOfOwner :many
-- How many documents the caller has of each type.
--
-- The ORDER BY names `documents.document_type` in full on purpose. The
-- select list calls its first column `document_type` too, and that one is
-- text: an unqualified `ORDER BY document_type` would be Postgres' rule that
-- an output name wins, and would sort the labels alphabetically. This sorts
-- by the enum, which is the order its labels were declared in (EXTENSION,
-- CRAWLED_URL, FILE, …), and that is the order of the keys in the answer.
SELECT documents.document_type::text AS document_type, count(documents.id) AS documents
FROM documents
WHERE documents.user_id = sqlc.arg(user_id)::uuid
GROUP BY documents.document_type
ORDER BY documents.document_type;

-- name: WatchedFoldersOfOwner :many
-- The caller's top-level folders marked as watched, in id order. `->>` reads
-- the value as text, so a folder whose metadata says "watched": "true" is
-- found as well as one that says true.
SELECT id, name, position, parent_id, created_at, updated_at, metadata
FROM folders
WHERE user_id = sqlc.arg(user_id)::uuid
  AND parent_id IS NULL
  AND metadata IS NOT NULL
  AND (metadata ->> 'watched') = 'true'
ORDER BY id;

-- name: RecentTitlesOfOwner :many
-- The title search for a title of fewer than two characters: the caller's
-- documents, most recently updated first. `updated_at` is nullable and
-- shared by a batch, so the id is what makes the order total. Both bounds
-- are bigint, and a negative one is Postgres' own refusal.
SELECT id, title, document_type::text AS document_type
FROM documents
WHERE user_id = sqlc.arg(user_id)::uuid
ORDER BY updated_at DESC NULLS LAST, id DESC
LIMIT sqlc.arg(page_limit)::bigint OFFSET sqlc.arg(page_offset)::bigint;

-- name: MatchingTitlesOfOwner :many
-- The same route for a longer title. `similarity` is pg_trgm's.
--
-- The threshold is `0.3::float8`, spelled out, because its type is a row.
-- `similarity()` returns a `real`, and three tenths as a real is a little
-- more than three tenths as a double. Compared in double precision, a title
-- whose similarity is exactly three tenths -- three shared trigrams of ten --
-- IS above the threshold, and is found. Written `0.3::real` it would not be:
-- "Tidings" is exactly three tenths like "tide", and a person typing "tide"
-- expects it.
--
-- The term is cast to text so that its two uses resolve alike.
SELECT id, title, document_type::text AS document_type
FROM documents
WHERE user_id = sqlc.arg(user_id)::uuid
  AND (similarity(title, sqlc.arg(term)::text) > 0.3::float8
       OR title ILIKE sqlc.arg(pattern)::text)
ORDER BY similarity(title, sqlc.arg(term)::text) DESC, title, id
LIMIT sqlc.arg(page_limit)::bigint OFFSET sqlc.arg(page_offset)::bigint;
