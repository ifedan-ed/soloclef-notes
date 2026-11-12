-- name: NextDocumentID :one
-- A document id taken before its row exists, because the stored file's key
-- carries it (documents/{account}/{document}/original/…) and the file is
-- streamed to storage before the transaction that inserts the row. An id
-- whose upload is then refused is simply never used; a sequence does not
-- hand a value out twice.
SELECT nextval('public.documents_id_seq')::bigint;

-- name: UploadNamed :one
-- The document an earlier upload of this name made in this account, with
-- the checksum of its newest original. Read before the file is streamed, to
-- decide which document id the storage key is built from. The identity is
-- unique per account (uq_documents_owner_identity), so the owner is named:
-- the same file is two accounts' two documents.
SELECT d.id,
       COALESCE(d.status ->> 'state', 'ready')::text AS state,
       (SELECT f.checksum_sha256
        FROM document_files AS f
        WHERE f.document_id = d.id AND f.kind = 'ORIGINAL'
        ORDER BY f.id DESC
        LIMIT 1) AS checksum
FROM documents AS d
WHERE d.user_id = sqlc.arg(user_id) AND d.unique_identifier_hash = sqlc.arg(identity)::text;

-- name: LockUploadNamed :one
-- The same read inside the writing transaction, holding the row: what is
-- decided from it cannot be changed by another upload or a delete before
-- this transaction commits.
SELECT d.id,
       COALESCE(d.status ->> 'state', 'ready')::text AS state,
       (SELECT f.checksum_sha256
        FROM document_files AS f
        WHERE f.document_id = d.id AND f.kind = 'ORIGINAL'
        ORDER BY f.id DESC
        LIMIT 1) AS checksum
FROM documents AS d
WHERE d.user_id = sqlc.arg(user_id) AND d.unique_identifier_hash = sqlc.arg(identity)::text
FOR UPDATE OF d;

-- name: InsertPendingUpload :one
-- A new document, waiting for the indexer. `content` is empty until the
-- indexer writes the parsed Markdown: a placeholder sentence there would
-- be shown, and found, as the document's text.
-- `content_hash` is the stored file's SHA-256, the one true thing known
-- about the content so far. ON CONFLICT covers two uploads of one name by
-- one account racing: the loser gets no row back and is told so. Another
-- account's document of the same identity is no conflict at all.
--
-- The type and the folder are the two things the routes differ in: a plain
-- upload is a FILE in no folder, and a file of an uploaded folder is a
-- LOCAL_FOLDER_FILE in the folder its path names. `use_vision_llm` is the
-- upload's vision switch, kept on the row so every later index of the
-- document describes its pictures too.
INSERT INTO documents (
    id, title, document_type, document_metadata, content, content_hash,
    unique_identifier_hash, user_id, folder_id, status, parser, use_vision_llm,
    created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(title), sqlc.arg(document_type)::text::documenttype, sqlc.arg(metadata)::jsonb, '', sqlc.arg(checksum),
    sqlc.arg(identity)::text, sqlc.arg(user_id), sqlc.narg(folder_id)::bigint,
    '{"state": "pending"}'::jsonb, sqlc.arg(parser)::text, sqlc.arg(use_vision_llm)::boolean,
    now(), now()
)
ON CONFLICT (user_id, unique_identifier_hash) DO NOTHING
RETURNING id;

-- name: RequeueUpload :execrows
-- An existing document handed back to the indexer: a failed one tried
-- again, or one whose file was uploaded again with different bytes. Its
-- text and chunks are left alone, so it stays searchable as it was until
-- the indexer replaces them. The metadata keys this upload knows are
-- merged over the old ones; the rest are kept. A folder upload names the
-- folder the file is in now; a plain upload names none and the document
-- stays where it is. The owner is named again although the id came from a
-- read that named it: no write here reaches a row by its id alone. The
-- parser and the vision switch are this upload's: the file is read the way
-- it was asked to be read now.
UPDATE documents
SET status = '{"state": "pending"}'::jsonb,
    parser = sqlc.arg(parser)::text,
    use_vision_llm = sqlc.arg(use_vision_llm)::boolean,
    content_hash = sqlc.arg(checksum),
    document_metadata = COALESCE(document_metadata, '{}'::jsonb) || sqlc.arg(metadata)::jsonb,
    folder_id = COALESCE(sqlc.narg(folder_id)::bigint, folder_id),
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: TouchUnchangedFolderFile :exec
-- A file of an uploaded folder whose bytes are the ones the document
-- already holds. Nothing is indexed again, but two things are recorded:
-- when it was last seen (`mtime` and the rest of this upload's metadata,
-- merged over the old keys), which is what the folder sync's "which files
-- changed" check reads, and the folder it is in now. `updated_at` is left
-- alone: the document did not change.
UPDATE documents
SET document_metadata = COALESCE(document_metadata, '{}'::jsonb) || sqlc.arg(metadata)::jsonb,
    folder_id = sqlc.arg(folder_id)::bigint
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: LockFolderIn :one
-- The folder a folder upload was told to put its files under, if it is
-- the caller's, held for the rest of the transaction so that it cannot
-- be deleted between this read and the documents being placed in it.
SELECT id FROM folders WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) FOR SHARE;

-- name: FolderIn :one
-- The same question before any file is read, so that a folder that is not
-- there refuses the upload while it has cost the sender nothing. Another
-- account's folder is not there.
SELECT id FROM folders WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: MarkFolderWatched :exec
-- The top folder of an uploaded folder says so: the desktop app lists the
-- folders it keeps in step by this marker, and reads the parser its files
-- were sent to from it. The keys are merged over whatever the folder
-- already carries, and `updated_at` moves because the folder was synced.
UPDATE folders
SET metadata = COALESCE(metadata, '{}'::jsonb) || sqlc.arg(metadata)::jsonb,
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: InsertOriginalFile :exec
-- Where the uploaded bytes are. The backend is recorded beside the key
-- because a file is only ever read from the store it was written to. The
-- indexer reads a document's newest ORIGINAL, so a replaced file is a new
-- row, not an edit of the old one. The pair (document, owner) is a foreign
-- key, so a file row cannot name one account and another's document.
INSERT INTO document_files (
    document_id, user_id, kind, storage_backend, storage_key,
    original_filename, mime_type, size_bytes, checksum_sha256, created_at
) VALUES (
    sqlc.arg(document_id), sqlc.arg(user_id), 'ORIGINAL', sqlc.arg(storage_backend), sqlc.arg(storage_key),
    sqlc.arg(original_filename), sqlc.narg(mime_type), sqlc.arg(size_bytes), sqlc.arg(checksum),
    now()
);
