-- The statements of the notebook research box. Every one names the owner:
-- the notebook, the document found again, the document made and the link
-- between them are each the caller's or they are not found.

-- name: NotebookIsOwned :one
SELECT EXISTS (
    SELECT 1 FROM notebooks WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id)
);

-- name: TouchNotebook :execrows
-- The caller's notebook, marked as used now: the notebook list is ordered by
-- it. Its row count is also how the import learns the notebook is the
-- caller's, inside the transaction that then writes to it.
UPDATE notebooks SET updated_at = sqlc.arg(used_at)
WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- name: ExistingImport :one
-- A document the account already holds for this page or this paper: the
-- same import identity, the same address, or the same paper under another
-- address (PubMed and a preprint server both list it). The oldest wins, so
-- two imports of one paper always link the same document. An id argument
-- that is empty matches nothing.
SELECT id FROM documents
WHERE user_id = sqlc.arg(user_id)
  AND status ->> 'state' <> 'deleting'
  AND (unique_identifier_hash = sqlc.arg(identity)::text
       OR document_metadata ->> 'url' = sqlc.arg(url)::text
       OR (sqlc.arg(doi)::text <> '' AND document_metadata -> 'paper' ->> 'doi' = sqlc.arg(doi)::text)
       OR (sqlc.arg(pmid)::text <> '' AND document_metadata -> 'paper' ->> 'pmid' = sqlc.arg(pmid)::text)
       OR (sqlc.arg(pmcid)::text <> '' AND document_metadata -> 'paper' ->> 'pmcid' = sqlc.arg(pmcid)::text)
       OR (sqlc.arg(arxiv_id)::text <> '' AND document_metadata -> 'paper' ->> 'arxiv_id' = sqlc.arg(arxiv_id)::text))
ORDER BY id
LIMIT 1;

-- name: InsertPending :one
-- A picked result as a document waiting to be read. It is listed among the
-- notebook's sources from this moment; its text arrives when the page has
-- been read. A second import of the same page racing this one inserts
-- nothing and returns no row: the identity is unique per account. The
-- parser is the one the import named, NULL when it named none; it reads
-- the address's file if the address turns out to be a PDF (StorePDF).
INSERT INTO documents (user_id, title, document_type, content, content_hash,
                       unique_identifier_hash, document_metadata, status, parser, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(title), 'CRAWLED_URL', sqlc.arg(snippet)::text,
        sqlc.arg(content_hash), sqlc.arg(identity)::text, sqlc.arg(metadata)::jsonb,
        '{"state": "pending", "stage": "reading"}'::jsonb, sqlc.narg(parser)::text, sqlc.arg(now)::timestamptz)
ON CONFLICT (user_id, unique_identifier_hash) DO NOTHING
RETURNING id;

-- name: LinkSource :exec
-- The document as a source of the notebook, ticked. Importing a source the
-- notebook has switched off turns it back on: picking it again is asking
-- for it.
INSERT INTO notebook_sources (notebook_id, user_id, document_id, enabled)
VALUES (sqlc.arg(notebook_id), sqlc.arg(user_id), sqlc.arg(document_id), true)
ON CONFLICT ON CONSTRAINT uq_notebook_sources_document DO UPDATE SET enabled = true;

-- name: RequeueUnread :execrows
-- An import whose page was never read (the process stopped, or the read was
-- interrupted and marked failed) is made to wait again, so that importing
-- it a second time reads it. Only a document this package made and never
-- filled: `fetched` is written with the text, read or not.
UPDATE documents
SET status = '{"state": "pending", "stage": "reading"}'::jsonb, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND document_metadata ? 'research_source' AND NOT (document_metadata ? 'fetched')
  AND (status ->> 'state' = 'failed'
       OR (status ->> 'stage' = 'reading' AND updated_at < sqlc.arg(stale_before)::timestamptz));

-- name: ClaimForReading :one
-- The document, taken to be read: only one that is still waiting for its
-- page, so two readers never fill one document.
UPDATE documents
SET status = '{"state": "processing", "stage": "reading"}'::jsonb, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status ->> 'state' = 'pending' AND status ->> 'stage' = 'reading'
RETURNING title, document_metadata;

-- name: StoreRead :execrows
-- What was read becomes the document's text, and the document waits for its
-- indexing job, which is queued after this commits. The metadata is merged:
-- what the import wrote stays, and what the read learned is added.
UPDATE documents
SET title = sqlc.arg(title), content = sqlc.arg(markdown)::text, source_markdown = sqlc.arg(markdown)::text,
    content_hash = sqlc.arg(content_hash), content_needs_reindexing = true,
    document_metadata = COALESCE(document_metadata, '{}'::jsonb) || sqlc.arg(learned)::jsonb,
    status = '{"state": "pending"}'::jsonb, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status ->> 'state' = 'processing' AND status ->> 'stage' = 'reading';

-- name: StorePDF :execrows
-- The address was a PDF, and it is now stored as the document's original
-- file (InsertOriginal, in the same transaction). The document waits for
-- its indexing job, which parses that file as it parses an uploaded PDF:
-- with the parser the import named, else the local one (pymupdf4llm), the
-- rule a PDF link was given. The row's own text is not newer than the file
-- (content_needs_reindexing), so the file is what is indexed. The metadata
-- is merged as StoreRead merges it.
UPDATE documents
SET title = sqlc.arg(title), content_hash = sqlc.arg(checksum),
    parser = COALESCE(parser, 'local'), content_needs_reindexing = false,
    document_metadata = COALESCE(document_metadata, '{}'::jsonb) || sqlc.arg(learned)::jsonb,
    status = '{"state": "pending"}'::jsonb, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status ->> 'state' = 'processing' AND status ->> 'stage' = 'reading';

-- name: InsertOriginal :exec
-- Where the PDF's bytes are, as an upload records its file: the indexer
-- and the viewer both read a document's newest ORIGINAL. The pair
-- (document, owner) is a foreign key, so the row cannot name one account
-- and another's document.
INSERT INTO document_files (
    document_id, user_id, kind, storage_backend, storage_key,
    original_filename, mime_type, size_bytes, checksum_sha256, created_at
) VALUES (
    sqlc.arg(document_id), sqlc.arg(user_id), 'ORIGINAL', sqlc.arg(storage_backend), sqlc.arg(storage_key),
    sqlc.arg(original_filename), 'application/pdf', sqlc.arg(size_bytes), sqlc.arg(checksum),
    sqlc.arg(now)::timestamptz
);

-- name: MarkUnread :execrows
-- A read that was cut short by the server stopping. The reason says what to
-- do, and RequeueUnread is what doing it runs.
UPDATE documents
SET status = jsonb_build_object('state', 'failed', 'reason', sqlc.arg(reason)::text),
    updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status ->> 'stage' = 'reading';
