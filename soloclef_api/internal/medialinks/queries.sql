-- Media links. Both statements name the owner: a file of another account's
-- artifact is a row that is not there.

-- name: MediaFilesOfOwner :many
-- Of the files named, the caller's own audio and video. Anything else (a
-- document, another account's file, a file that is gone, a file whose
-- artifact is being deleted) is simply not listed, so a report whose
-- embedded output was deleted still opens with that one player missing.
SELECT f.id, f.artifact_id, f.mime_type
FROM artifact_files f
JOIN artifacts a ON a.id = f.artifact_id
JOIN documents d ON d.id = a.document_id AND d.user_id = a.user_id
WHERE a.user_id = sqlc.arg(user_id)
  AND f.id = ANY(sqlc.arg(file_ids)::bigint[])
  AND split_part(f.mime_type, '/', 1) IN ('audio', 'video')
  AND d.status ->> 'state' <> 'deleting'
ORDER BY f.id;

-- name: LinkedMedia :one
-- The file a link names, read again at every use under the account the
-- link was made for: that account must still be active, the file must
-- still be that artifact's and still be audio or video, and the artifact
-- must not be on its way out.
SELECT f.storage_backend, f.storage_key, f.original_filename, f.mime_type, f.checksum_sha256
FROM artifact_files f
JOIN artifacts a ON a.id = f.artifact_id
JOIN documents d ON d.id = a.document_id AND d.user_id = a.user_id
JOIN "user" u ON u.id = a.user_id AND u.is_active
WHERE f.id = sqlc.arg(file_id) AND f.artifact_id = sqlc.arg(artifact_id)
  AND a.user_id = sqlc.arg(user_id)
  AND split_part(f.mime_type, '/', 1) IN ('audio', 'video')
  AND d.status ->> 'state' <> 'deleting';
