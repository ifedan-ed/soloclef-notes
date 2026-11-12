-- Asking for a Google Drive sync: the import of picked files, and "sync
-- now". Every statement names the account beside the row's id.

-- name: OwnedConnection :one
-- The caller's own connection of any kind, for "sync now": its kind says
-- whether there is anything to sync. Another account's connection and a
-- missing one are the same no row.
SELECT id, connector_type::text AS kind, config, enabled, parser
FROM search_source_connectors
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: OwnedNotebook :one
-- The notebook an import links to must be the caller's own.
SELECT id FROM notebooks WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: TouchNotebook :execrows
-- Activity, so the notebook rises in the recent list. It runs in the
-- import's transaction, and a notebook deleted since the route looked
-- touches no row, which is how the import learns of it before it links
-- anything.
UPDATE notebooks SET updated_at = now() WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: LockImported :one
-- The document an earlier import of this Drive file made in this account
-- (the identity is unique per account, uq_documents_owner_identity), held
-- for the rest of the transaction.
SELECT id,
       COALESCE(status ->> 'state', 'ready')::text AS state,
       COALESCE(status ->> 'stage', '')::text AS stage
FROM documents
WHERE user_id = sqlc.arg(user_id) AND unique_identifier_hash = sqlc.arg(identity)::text
FOR UPDATE;

-- name: InsertPending :one
-- A new document waiting for its file. `content` is empty until the
-- indexer writes the parsed Markdown, and `content_hash` a stated
-- placeholder until the worker has the bytes. It names no parser: a Drive
-- document is read with its connection's. ON CONFLICT covers two imports
-- of one file by one account racing: the loser gets no row back and reads
-- the winner's. The connection is named through a subquery on its owner,
-- so a document can never point at another account's connection.
INSERT INTO documents (
    title, document_type, document_metadata, content, content_hash,
    unique_identifier_hash, user_id, connector_id, status, created_at, updated_at
) VALUES (
    sqlc.arg(title), 'GOOGLE_DRIVE_FILE', sqlc.arg(metadata)::jsonb, '', sqlc.arg(placeholder_hash),
    sqlc.arg(identity)::text, sqlc.arg(user_id),
    (SELECT c.id FROM search_source_connectors AS c
     WHERE c.id = sqlc.arg(connector_id) AND c.user_id = sqlc.arg(user_id)),
    '{"state": "pending", "stage": "downloading"}'::jsonb, now(), now()
)
ON CONFLICT (user_id, unique_identifier_hash) DO NOTHING
RETURNING id;

-- name: OweAgain :execrows
-- An existing document set waiting for its file again: one that failed, or
-- one already waiting (whose clock is restarted, so that the sync of
-- everything does not take it for abandoned while this import's job is on
-- its way). Its text and chunks are untouched. A ready document is not
-- matched: whether its file changed is the worker's to find out.
UPDATE documents
SET status = '{"state": "pending", "stage": "downloading"}'::jsonb, updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND (status ->> 'state' = 'failed'
       OR (status ->> 'state' = 'pending' AND status ->> 'stage' = 'downloading'));

-- name: LinkDocumentToNotebook :exec
-- Link it, or switch a switched-off link back on. The link carries the
-- owner, and its composite foreign keys make the notebook, the document
-- and the link one account's or the insert fails.
INSERT INTO notebook_sources (notebook_id, user_id, document_id, enabled)
VALUES (sqlc.arg(notebook_id), sqlc.arg(user_id), sqlc.arg(document_id)::bigint, true)
ON CONFLICT ON CONSTRAINT uq_notebook_sources_document DO UPDATE SET enabled = true;

-- name: MarkNotQueued :execrows
-- The job could not be published: the documents still waiting for it say
-- so, instead of waiting for ever. Only a document still waiting is
-- marked.
UPDATE documents
SET status = jsonb_build_object('state', 'failed', 'reason', sqlc.arg(reason)::text), updated_at = now()
WHERE id = ANY(sqlc.arg(ids)::bigint[]) AND user_id = sqlc.arg(user_id)
  AND status ->> 'state' = 'pending' AND status ->> 'stage' = 'downloading';
