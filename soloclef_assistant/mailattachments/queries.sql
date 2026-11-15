-- The rows behind a file a mail draft carries: a Studio output's file or a
-- document's original upload. Every statement names the owner, so a file
-- id or a document id of another account finds nothing.

-- name: ArtifactFileOfDocument :one
-- A Studio output is a document whose artifact holds the file itself (the
-- primary one; the other role is its preview picture).
SELECT file.id, file.storage_backend, file.storage_key, file.original_filename, file.mime_type,
       file.size_bytes, file.checksum_sha256, document.title
FROM artifacts AS artifact
JOIN artifact_files AS file ON file.artifact_id = artifact.id AND file.role = 'primary'
JOIN documents AS document ON document.id = artifact.document_id AND document.user_id = artifact.user_id
WHERE artifact.document_id = sqlc.arg(document_id) AND artifact.user_id = sqlc.arg(user_id);

-- name: ArtifactFileOfArtifact :one
-- The same, by the artifact's own id, which the Studio's result names.
SELECT file.id, file.storage_backend, file.storage_key, file.original_filename, file.mime_type,
       file.size_bytes, file.checksum_sha256, document.title, document.id AS document_id
FROM artifacts AS artifact
JOIN artifact_files AS file ON file.artifact_id = artifact.id AND file.role = 'primary'
JOIN documents AS document ON document.id = artifact.document_id AND document.user_id = artifact.user_id
WHERE artifact.id = sqlc.arg(artifact_id) AND artifact.user_id = sqlc.arg(user_id);

-- name: OriginalFileOfDocument :one
-- A document's upload as it arrived; the newest if it was replaced.
SELECT file.id, file.storage_backend, file.storage_key, file.original_filename, file.mime_type,
       file.size_bytes, file.checksum_sha256, document.title
FROM document_files AS file
JOIN documents AS document ON document.id = file.document_id AND document.user_id = file.user_id
WHERE file.document_id = sqlc.arg(document_id) AND file.user_id = sqlc.arg(user_id) AND file.kind = 'ORIGINAL'
ORDER BY file.created_at DESC, file.id DESC
LIMIT 1;

-- name: DocumentsTitled :many
-- The owner's documents a title names: by the title, or by the name the
-- file was uploaded under. A few, newest first: more than one is a
-- question for the model, which is shown them to choose by id.
SELECT document.id, document.title, document.created_at
FROM documents AS document
WHERE document.user_id = sqlc.arg(user_id)
  AND (lower(document.title) = lower(sqlc.arg(title))
       OR EXISTS (SELECT 1 FROM document_files AS file
                  WHERE file.document_id = document.id AND file.user_id = document.user_id
                    AND file.kind = 'ORIGINAL' AND lower(file.original_filename) = lower(sqlc.arg(title))))
ORDER BY document.created_at DESC, document.id DESC
LIMIT 6;

-- name: ArtifactFileByID :one
SELECT file.storage_backend, file.storage_key, file.size_bytes, file.checksum_sha256
FROM artifact_files AS file
JOIN artifacts AS artifact ON artifact.id = file.artifact_id
WHERE file.id = sqlc.arg(id) AND artifact.user_id = sqlc.arg(user_id);

-- name: DocumentFileByID :one
SELECT storage_backend, storage_key, size_bytes, checksum_sha256
FROM document_files
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);
