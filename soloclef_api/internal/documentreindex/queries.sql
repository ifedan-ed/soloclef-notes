-- name: ReindexSubject :one
-- What a re-index is decided from, for the account that owns the document
-- (another account's document is no row, as an id that names nothing is):
-- what state it is in, the name of its newest uploaded original (the name is what a parser
-- is matched against), and whether it has text of its own to index when it
-- has no file (a note, a crawled page). The name is '' for a document with
-- no original: a stored file always has a name, so '' cannot be mistaken
-- for one.
SELECT COALESCE(d.status ->> 'state', 'ready')::text AS state,
       COALESCE((SELECT f.original_filename
        FROM document_files AS f
        WHERE f.document_id = d.id AND f.kind = 'ORIGINAL'
        ORDER BY f.id DESC
        LIMIT 1), '')::text AS filename,
       (COALESCE(d.source_markdown, '') <> '')::boolean AS has_text
FROM documents AS d
WHERE d.id = sqlc.arg(id) AND d.user_id = sqlc.arg(user_id)::uuid;

-- name: HandBackToIndexer :execrows
-- The document is set waiting, and given the parser named, if one was. It
-- is written only from a state in which no job is on its way and nothing is
-- deleting it, so two requests cannot both queue a job, and a re-index
-- cannot bring back a document that is being deleted. The text and chunks
-- are untouched: the document stays searchable as it was until the indexer
-- replaces them. The owner is named again, so this write can no more reach
-- another account's document than the read before it can.
UPDATE documents
SET status = '{"state": "pending"}'::jsonb,
    parser = COALESCE(sqlc.narg(parser)::text, parser),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)::uuid
  AND COALESCE(status ->> 'state', 'ready') NOT IN ('pending', 'processing', 'deleting');
