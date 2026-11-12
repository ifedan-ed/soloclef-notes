-- name: OwnedNextcloudConnection :one
-- The connection's stored settings, for the account that owns it. The app
-- password in them is that account's, so nobody else's request reads the
-- row at all: another account's connection and a missing one look the same.
SELECT id, config
FROM search_source_connectors
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(owner)::uuid
  AND connector_type = 'NEXTCLOUD_CONNECTOR';

-- name: OwnedNotebook :one
-- The notebook an import links to must be the caller's own; another
-- account's is as absent as one that does not exist.
SELECT id FROM notebooks WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: TouchNotebook :execrows
-- Activity, so the notebook rises in the recent list. The owner is named
-- here too: this runs in the import's transaction, after the check above
-- and not under its lock, and a notebook deleted in between touches no row,
-- which is how the import learns of it before it links anything.
UPDATE notebooks SET updated_at = now() WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: LockImported :one
-- The document an earlier import of this file made in this account (the
-- identity is unique per account, uq_documents_owner_identity), held for
-- the rest of the transaction. `stage` is 'downloading' while the document waits for
-- this tier to fetch its file, and `stale` says it has waited longer than
-- the interval given: the process that owed the download is gone.
SELECT id,
       COALESCE(status ->> 'state', 'ready')::text AS state,
       COALESCE(status ->> 'stage', '')::text AS stage,
       COALESCE(document_metadata #>> '{nextcloud,etag}', '')::text AS etag,
       (COALESCE(updated_at, created_at) < now() - make_interval(secs => sqlc.arg(stale_seconds)::float8))::boolean AS stale
FROM documents
WHERE user_id = sqlc.arg(user_id) AND unique_identifier_hash = sqlc.arg(identity)::text
FOR UPDATE;

-- name: InsertPendingImport :one
-- A new document waiting for its file to be downloaded. `content` is empty
-- until the indexer writes the parsed Markdown: a placeholder sentence
-- there would be shown, and found, as the document's text. `content_hash` is a stated placeholder until the bytes are in
-- hand, when it becomes their SHA-256 (RecordDownloaded). ON CONFLICT
-- covers two imports of one file by one account racing: the loser gets no
-- row back and reads the winner's. The folder, when one is named, must be
-- the same account's: the composite foreign key refuses any other.
INSERT INTO documents (
    title, document_type, document_metadata, content, content_hash,
    unique_identifier_hash, user_id, folder_id, status, parser, use_vision_llm,
    created_at, updated_at
) VALUES (
    sqlc.arg(title), 'FILE', sqlc.arg(metadata)::jsonb, '', sqlc.arg(placeholder_hash),
    sqlc.arg(identity)::text, sqlc.arg(user_id), sqlc.narg(folder_id),
    '{"state": "pending", "stage": "downloading"}'::jsonb, sqlc.arg(parser)::text, sqlc.arg(use_vision_llm)::boolean,
    now(), now()
)
ON CONFLICT (user_id, unique_identifier_hash) DO NOTHING
RETURNING id;

-- name: RequeueImport :exec
-- An existing document set waiting for its file again: one that failed,
-- one whose file changed in Nextcloud, or one whose download was abandoned.
-- Its text and chunks are untouched, so it stays searchable as it was until
-- the indexer replaces them. The metadata this import knows is merged over
-- the old; the rest is kept. The parser and the vision switch are this
-- import's.
UPDATE documents
SET status = '{"state": "pending", "stage": "downloading"}'::jsonb,
    parser = sqlc.arg(parser)::text,
    use_vision_llm = sqlc.arg(use_vision_llm)::boolean,
    document_metadata = COALESCE(document_metadata, '{}'::jsonb) || sqlc.arg(metadata)::jsonb,
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: PlaceInFolder :exec
-- A file picked again inside a folder moves to that folder.
UPDATE documents SET folder_id = sqlc.arg(folder_id) WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: LinkDocumentToNotebook :exec
-- Link it, or switch a switched-off link back on. The link carries the
-- owner, and its three composite foreign keys make the notebook, the
-- document and the link one account's or the insert fails.
INSERT INTO notebook_sources (notebook_id, user_id, document_id, enabled)
VALUES (sqlc.arg(notebook_id), sqlc.arg(user_id), sqlc.arg(document_id)::bigint, true)
ON CONFLICT ON CONSTRAINT uq_notebook_sources_document DO UPDATE SET enabled = true;

-- name: LinkFolderToNotebook :exec
INSERT INTO notebook_sources (notebook_id, user_id, folder_id, enabled)
VALUES (sqlc.arg(notebook_id), sqlc.arg(user_id), sqlc.arg(folder_id)::bigint, true)
ON CONFLICT ON CONSTRAINT uq_notebook_sources_folder DO UPDATE SET enabled = true;

-- name: RecordDownloaded :execrows
-- The file is in the store: the document stops waiting for its download and
-- starts waiting for the indexer. Written only while the document is still
-- waiting for this download, so a document deleted, or queued again and
-- downloaded by another job, is not touched; no row changed tells the
-- caller its copy is not wanted. The owner is the one the download's job
-- was made for.
UPDATE documents
SET status = '{"state": "pending"}'::jsonb,
    content_hash = sqlc.arg(checksum),
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status ->> 'state' = 'pending' AND status ->> 'stage' = 'downloading';

-- name: InsertImportedOriginal :exec
-- Where the downloaded bytes are. The indexer reads a document's newest
-- ORIGINAL, so a refreshed file is a new row, not an edit of the old one.
INSERT INTO document_files (
    document_id, user_id, kind, storage_backend, storage_key,
    original_filename, mime_type, size_bytes, checksum_sha256, created_at
) VALUES (
    sqlc.arg(document_id), sqlc.arg(user_id), 'ORIGINAL', sqlc.arg(storage_backend), sqlc.arg(storage_key),
    sqlc.arg(original_filename), sqlc.narg(mime_type), sqlc.arg(size_bytes), sqlc.arg(checksum),
    now()
);

-- name: MarkDownloadFailed :execrows
-- A document whose file could not be fetched, with a reason for the person
-- looking at the list. Only a document still waiting for this download is
-- marked: anything newer is the truth and this failure is not.
UPDATE documents
SET status = jsonb_build_object('state', 'failed', 'reason', left(sqlc.arg(reason)::text, 500)),
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status ->> 'state' = 'pending' AND status ->> 'stage' = 'downloading';

-- name: ImportedDocument :one
-- The caller's own document as a refresh decides from it: what it is
-- called, its state, and the Nextcloud facts its import recorded ('{}' for
-- a document that was not imported from Nextcloud). Another account's
-- document is no row, the same as an id never used.
SELECT title,
       COALESCE(status ->> 'state', 'ready')::text AS state,
       COALESCE(status ->> 'stage', '')::text AS stage,
       (COALESCE(updated_at, created_at) < now() - make_interval(secs => sqlc.arg(stale_seconds)::float8))::boolean AS stale,
       COALESCE(document_metadata -> 'nextcloud', '{}'::jsonb)::jsonb AS facts
FROM documents
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: QueueRefresh :execrows
-- Set the document waiting for its file again, with what Nextcloud says of
-- the file now merged into its recorded facts. Written only from a state in
-- which no job is on its way (or the one that was has been abandoned), so
-- two refreshes cannot both start a download.
UPDATE documents
SET status = '{"state": "pending", "stage": "downloading"}'::jsonb,
    document_metadata = jsonb_set(
        COALESCE(document_metadata, '{}'::jsonb), '{nextcloud}',
        COALESCE(document_metadata -> 'nextcloud', '{}'::jsonb) || sqlc.arg(facts)::jsonb),
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND (COALESCE(status ->> 'state', 'ready') NOT IN ('pending', 'processing', 'deleting')
       OR (status ->> 'state' = 'pending' AND status ->> 'stage' = 'downloading'
           AND COALESCE(updated_at, created_at) < now() - make_interval(secs => sqlc.arg(stale_seconds)::float8)));

-- name: RefreshableDocuments :many
-- The caller's documents that came from Nextcloud, each with the caller's
-- own connection that can read its file again: the one for the same
-- Nextcloud account on this deployment's server. One statement, so the
-- document list's question is one round trip however many documents there
-- are.
--
-- Both sides are the caller's (the join is on the owner, and the outer
-- WHERE names it), so a document is never paired with another account's
-- connection, whatever its metadata says. The lowest connection id is the
-- answer if an account ever has two for one Nextcloud account, so the
-- answer is the same every time. A document being deleted is left out.
SELECT d.id AS document_id, min(c.id)::bigint AS connector_id
FROM documents AS d
JOIN search_source_connectors AS c
  ON c.user_id = d.user_id
 AND c.connector_type = 'NEXTCLOUD_CONNECTOR'
 AND c.config ->> 'username' = d.document_metadata #>> '{nextcloud,username}'
 AND c.config ->> 'server_url' = d.document_metadata #>> '{nextcloud,server_url}'
WHERE d.user_id = sqlc.arg(user_id)
  AND d.document_type = 'FILE'
  AND COALESCE(d.document_metadata #>> '{nextcloud,path}', '') <> ''
  AND c.config ->> 'server_url' = sqlc.arg(server_url)::text
  AND COALESCE(d.status ->> 'state', 'ready') <> 'deleting'
GROUP BY d.id
ORDER BY d.id;
