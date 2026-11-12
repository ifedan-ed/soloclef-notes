-- name: DocumentsOfTypes :many
-- The owner's documents of the named types: the limit a search confined to
-- some types is given. Documents being deleted are left out here as they
-- are from every search.
SELECT id FROM documents
WHERE user_id = sqlc.arg(user_id)
  AND document_type::text = ANY(sqlc.arg(document_types)::text[])
  AND status ->> 'state' <> 'deleting'
ORDER BY id;
