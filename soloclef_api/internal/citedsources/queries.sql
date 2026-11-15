-- name: CitedPassages :many
-- The document each cited passage is in, with the page it starts on, for
-- the passages that are the caller's. The ids come out of message text, so
-- the owner is what makes them safe to ask about: another account's passage,
-- and a passage whose document is being deleted, find no row. One statement
-- for a whole answer's sources, reading only the two short columns a
-- "Sources" list needs, not the document's text.
SELECT c.id AS chunk_id, d.id AS document_id, d.title, c.page_start
FROM chunks c
JOIN documents d ON d.id = c.document_id AND d.user_id = c.user_id
WHERE c.id = ANY(sqlc.arg(chunk_ids)::bigint[]) AND c.user_id = sqlc.arg(user_id)::uuid
  AND d.status ->> 'state' <> 'deleting'
ORDER BY c.id;
