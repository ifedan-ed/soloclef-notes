-- The statements of a document's stored files. Each names the owner on the
-- document and again on the file, so a file row can never be reached through
-- another account's document, nor another account's file through one's own.
-- A document being deleted is already gone as far as a person can tell.

-- name: ManifestOf :one
-- The document and its original file, if it has one, in one statement. A
-- document re-uploaded keeps its older originals; the newest is the one a
-- person means.
SELECT d.id, d.title, d.document_type::text AS document_type, (d.status ->> 'state')::text AS state,
       -- The join is outer, so the file's columns are given a value a missing
       -- file can have: id 0, which no file row has, is "no original".
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
WHERE d.id = sqlc.arg(document_id) AND d.user_id = sqlc.arg(user_id)
  AND d.status ->> 'state' <> 'deleting';

-- name: OriginalOf :one
-- Where the newest original's bytes are. No row means the document is not
-- the caller's or has no original; DocumentIsOwned tells the two apart.
SELECT f.storage_backend, f.storage_key, f.original_filename, f.mime_type, f.checksum_sha256
FROM document_files f
JOIN documents d ON d.id = f.document_id AND d.user_id = f.user_id
WHERE f.document_id = sqlc.arg(document_id) AND f.user_id = sqlc.arg(user_id) AND f.kind = 'ORIGINAL'
  AND d.status ->> 'state' <> 'deleting'
ORDER BY f.created_at DESC, f.id DESC
LIMIT 1;

-- name: FileOf :one
-- One stored file by its id, which is what the manifest's content_url names.
SELECT f.storage_backend, f.storage_key, f.original_filename, f.mime_type, f.checksum_sha256
FROM document_files f
JOIN documents d ON d.id = f.document_id AND d.user_id = f.user_id
WHERE f.id = sqlc.arg(file_id) AND f.document_id = sqlc.arg(document_id) AND f.user_id = sqlc.arg(user_id)
  AND d.status ->> 'state' <> 'deleting';

-- name: DocumentIsOwned :one
SELECT EXISTS (
    SELECT 1 FROM documents
    WHERE id = sqlc.arg(document_id) AND user_id = sqlc.arg(user_id) AND status ->> 'state' <> 'deleting'
);
