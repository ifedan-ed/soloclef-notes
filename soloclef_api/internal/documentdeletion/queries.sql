-- name: OwnsDocument :one
-- Whether this account has this document. The route asks before it removes
-- anything, and the answer for another account's document is the answer for
-- an id that names nothing: no row.
SELECT true::boolean AS owned FROM documents WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)::uuid;

-- The five statements below are the removal's, and name a document by id
-- alone. Every route that removes a document has read the row with its
-- owner first (OwnsDocument here; the note, artifact and connection deletes
-- in their own packages), and each statement names the document id, so none
-- can reach another document's rows. Each says so in its own comment.

-- name: MarkDocumentDeleting :execrows
-- owner: through the caller, which read the document with its owner.
-- The marker replaces the status outright. It is what takes the document
-- out of search at once, and what tells the indexing worker to leave it
-- alone (indexstore.Load, Begin and Replace all read it). Zero rows means
-- the document is already gone.
UPDATE documents SET status = '{"state": "deleting"}'::jsonb WHERE id = $1;

-- name: ChunkIDsOf :many
-- owner: through the caller, which read the document with its owner.
-- One batch of the document's chunk ids, lowest first. The removal takes a
-- batch, removes those points from the vector store, deletes those rows,
-- and asks again, so the memory and the statement time of a delete do not
-- grow with the document.
SELECT id FROM chunks WHERE document_id = sqlc.arg(document_id) ORDER BY id LIMIT sqlc.arg(batch);

-- name: DeleteChunks :exec
-- owner: through the caller, which read the document with its owner.
-- The rows of a batch whose points are already gone. The document id is
-- repeated so that an id list can never reach another document's chunks.
DELETE FROM chunks WHERE document_id = sqlc.arg(document_id) AND id = ANY(sqlc.arg(ids)::bigint[]);

-- name: FilesOf :many
-- owner: through the caller, which read the document with its owner.
-- Every stored file of the document, with the backend each was written to:
-- a file is removed from the store its row names and from no other.
--
-- Three places name a file, and the document row is the only way to any of
-- them, so all three are read before it goes:
--
--   - document_files: the upload and what was made from it;
--   - artifact_files, when the document is an artifact's: the thing itself
--     (the MP4, the deck, the episode) and its preview. This used to be
--     left out, so deleting an artifact deleted its rows and kept its files;
--   - a video's narration, one MP3 a slide, which has no row at all: the
--     artifact's metadata names each under slides[].audio_storage_key with
--     the store it is in (soloclef_worker/internal/videojob/artifact.go).
--
-- A slide that names audio and no store is returned with an empty backend,
-- which the removal refuses: guessing a store would delete from the wrong
-- one, and skipping the file would orphan it. Metadata whose slides are not
-- a list names no audio.
SELECT d.storage_backend::text AS storage_backend, d.storage_key::text AS storage_key
FROM document_files AS d
WHERE d.document_id = sqlc.arg(document_id)
UNION ALL
SELECT f.storage_backend::text, f.storage_key::text
FROM artifact_files AS f
JOIN artifacts AS a ON a.id = f.artifact_id
WHERE a.document_id = sqlc.arg(document_id)
UNION ALL
SELECT COALESCE(slide.value ->> 'storage_backend', '')::text, (slide.value ->> 'audio_storage_key')::text
FROM artifacts AS a
CROSS JOIN LATERAL jsonb_array_elements(
    CASE WHEN jsonb_typeof(a.metadata -> 'slides') = 'array' THEN a.metadata -> 'slides' ELSE '[]'::jsonb END
) AS slide (value)
WHERE a.document_id = sqlc.arg(document_id)
  AND COALESCE(slide.value ->> 'audio_storage_key', '') <> '';

-- name: DeleteDocument :exec
-- owner: through the caller, which read the document with its owner.
-- The row itself, last. Its file rows, its artifact and that artifact's
-- file rows, versions, source guides and notebook links go with it by ON DELETE CASCADE. Only a document that is
-- marked is deleted: the marker is this removal's own, and a row without it
-- is not one this removal was asked to finish.
DELETE FROM documents WHERE id = $1 AND status ->> 'state' = 'deleting';
