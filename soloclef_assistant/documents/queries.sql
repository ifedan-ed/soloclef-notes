-- The statements behind the chat's search of an account's own documents.
-- Every one names the account: a chunk id that came back from the vector
-- store is never trusted to belong to the caller's account, it is re-read
-- under this filter.

-- name: KeywordRanks :many
-- The keyword leg: chunks whose text matches the query's words, ranked. The
-- expression is the one the GIN index ix_chunks_content_search is built on, so it
-- is an index scan. `rank()` gives ties the same rank, which is what the
-- fusion expects of both legs. A document being deleted is not searched.
SELECT ranked.id, ranked.rank::bigint AS rank
FROM (
    SELECT chunk.id,
           rank() OVER (
               ORDER BY ts_rank_cd(to_tsvector('english', chunk.content),
                                   plainto_tsquery('english', sqlc.arg(query)::text)) DESC
           ) AS rank
    FROM chunks AS chunk
    JOIN documents AS document ON document.id = chunk.document_id
    WHERE chunk.user_id = sqlc.arg(user_id)
      AND to_tsvector('english', chunk.content) @@ plainto_tsquery('english', sqlc.arg(query)::text)
      AND document.status ->> 'state' <> 'deleting'
      AND (sqlc.arg(all_documents)::boolean OR chunk.document_id = ANY (sqlc.arg(document_ids)::bigint[]))
) AS ranked
ORDER BY ranked.rank, ranked.id
LIMIT sqlc.arg(row_limit);

-- name: ChunksByID :many
-- The candidates, read back by id with the access rules applied again: the
-- account, the document scope, and not being deleted.
SELECT chunk.id, chunk.content, chunk."position", chunk.page_start,
       document.id AS document_id, document.title,
       document.document_type::text AS document_type,
       document.document_metadata
FROM chunks AS chunk
JOIN documents AS document ON document.id = chunk.document_id
WHERE chunk.user_id = sqlc.arg(user_id)
  AND chunk.id = ANY (sqlc.arg(chunk_ids)::bigint[])
  AND document.status ->> 'state' <> 'deleting'
  AND (sqlc.arg(all_documents)::boolean OR chunk.document_id = ANY (sqlc.arg(document_ids)::bigint[]))
ORDER BY chunk.id;

-- name: ChunkIDsOfDocuments :many
-- A search confined to some documents is confined, in the vector store, by
-- point id, and a point's id is its chunk's. Ordered so the request is the
-- same bytes for the same scope.
SELECT id FROM chunks
WHERE user_id = sqlc.arg(user_id)
  AND document_id = ANY (sqlc.arg(document_ids)::bigint[])
ORDER BY id;

-- name: DocumentsInFolders :many
-- A mentioned folder means the documents directly in it.
SELECT id FROM documents
WHERE user_id = sqlc.arg(user_id)
  AND folder_id = ANY (sqlc.arg(folder_ids)::bigint[])
ORDER BY id;
