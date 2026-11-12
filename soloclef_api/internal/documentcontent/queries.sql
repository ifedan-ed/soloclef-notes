-- The one statement of the document text read. It names the owner beside the
-- id, so another account's document is the same "not found" as one never
-- made, and it counts the chunks in the same statement rather than a second.

-- name: DocumentText :one
SELECT d.id, d.title, d.document_type::text AS document_type, d.content,
       d.source_markdown, d.status, d.updated_at, d.created_at,
       (SELECT count(*) FROM chunks c WHERE c.document_id = d.id AND c.user_id = d.user_id) AS chunk_count
FROM documents d
WHERE d.id = sqlc.arg(id) AND d.user_id = sqlc.arg(user_id);
