-- Every statement here names the owner. A folder or a document is the
-- caller's or it does not exist: there is no second check after the read,
-- so there is nothing to forget, and another account's row answers exactly
-- as a row that was never made.

-- name: FolderOwned :one
-- One folder of this account. The row a route is about, and equally the
-- parent or the target a write names: all of them must be the caller's.
SELECT id, name, position, parent_id, created_at, updated_at, metadata
FROM folders
WHERE id = sqlc.arg('folder_id') AND user_id = sqlc.arg('user_id');

-- name: FoldersOwned :many
-- The account's whole tree, flat. `position` ties (every first child of
-- every parent gets the same first key), so `id` is what makes the order
-- total and the same on every request.
SELECT id, name, position, parent_id, created_at, updated_at, metadata
FROM folders
WHERE user_id = sqlc.arg('user_id')
ORDER BY position, id;

-- name: FolderAncestors :many
-- The chain from the root down to the folder itself: the breadcrumb, and the
-- set a move asks "is the folder being moved among the target's ancestors?"
-- of. The owner is named on every step, not only the first, so the walk can
-- never leave the account even if a parent link were ever wrong.
WITH RECURSIVE ancestors AS (
    SELECT f.id, f.name, f.parent_id, 0 AS depth
    FROM folders f
    WHERE f.id = sqlc.arg('folder_id') AND f.user_id = sqlc.arg('user_id')
    UNION ALL
    SELECT f.id, f.name, f.parent_id, a.depth + 1
    FROM folders f
    JOIN ancestors a ON f.id = a.parent_id
    WHERE f.user_id = sqlc.arg('user_id')
)
SELECT a.id, a.name FROM ancestors a ORDER BY a.depth DESC;

-- name: FolderDepth :one
-- How deep a folder sits: 1 for a top-level folder, 0 for one that is not
-- this account's.
WITH RECURSIVE ancestors AS (
    SELECT f.id, f.parent_id, 1 AS depth
    FROM folders f
    WHERE f.id = sqlc.arg('folder_id') AND f.user_id = sqlc.arg('user_id')
    UNION ALL
    SELECT f.id, f.parent_id, a.depth + 1
    FROM folders f
    JOIN ancestors a ON f.id = a.parent_id
    WHERE f.user_id = sqlc.arg('user_id')
)
SELECT COALESCE(MAX(a.depth), 0)::int AS depth FROM ancestors a;

-- name: SubtreeDepth :one
-- How many levels hang below a folder: 0 for a leaf.
WITH RECURSIVE descendants AS (
    SELECT f.id, 1 AS depth
    FROM folders f
    WHERE f.parent_id = sqlc.arg('folder_id')::bigint AND f.user_id = sqlc.arg('user_id')
    UNION ALL
    SELECT f.id, d.depth + 1
    FROM folders f
    JOIN descendants d ON f.parent_id = d.id
    WHERE f.user_id = sqlc.arg('user_id')
)
SELECT COALESCE(MAX(d.depth), 0)::int AS depth FROM descendants d;

-- name: LastSiblingPosition :one
-- The last key among the folders under one parent (NULL for the top level),
-- which a new or a moved folder is placed after. Sorted by Postgres and not
-- in Go because the keys are compared by the column's collation everywhere
-- else they are ordered.
SELECT position FROM folders
WHERE user_id = sqlc.arg('user_id')
  AND (parent_id = sqlc.narg('parent_id')::bigint
       OR (sqlc.narg('parent_id')::bigint IS NULL AND parent_id IS NULL))
ORDER BY position DESC, id DESC
LIMIT 1;

-- name: InsertFolder :one
-- A new folder. The two times are passed and not left to the column
-- defaults so that the answer and the row carry the same instant, and so a
-- test can hold the clock still. A parent of another account cannot be
-- named: the composite foreign key (parent_id, user_id) refuses it.
INSERT INTO folders (user_id, name, position, parent_id, created_at, updated_at)
VALUES (sqlc.arg('user_id'), sqlc.arg('name'), sqlc.arg('position'),
        sqlc.narg('parent_id'), sqlc.arg('created_at'), sqlc.arg('updated_at'))
RETURNING id, name, position, parent_id, created_at, updated_at, metadata;

-- name: RenameFolder :execrows
UPDATE folders SET name = sqlc.arg('name'), updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('folder_id') AND user_id = sqlc.arg('user_id');

-- name: ReparentFolder :execrows
-- A move: the new parent and the position generated under it.
UPDATE folders
SET parent_id = sqlc.narg('parent_id'), position = sqlc.arg('position'),
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('folder_id') AND user_id = sqlc.arg('user_id');

-- name: RepositionFolder :execrows
UPDATE folders SET position = sqlc.arg('position'), updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('folder_id') AND user_id = sqlc.arg('user_id');

-- name: ClearWatchedFlag :execrows
-- Stop keeping a folder in step with a folder on someone's computer. Only
-- the one key is written, so every other value in the metadata keeps its own
-- text, and only a folder that is watched is touched: asking twice changes
-- nothing the second time, `updated_at` included.
UPDATE folders
SET metadata = jsonb_set(metadata, '{watched}', 'false'::jsonb),
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('folder_id') AND user_id = sqlc.arg('user_id')
  AND metadata -> 'watched' = 'true'::jsonb;

-- name: FolderSubtreeIDs :many
-- The folder and everything under it, the folder itself first.
WITH RECURSIVE subtree AS (
    SELECT f.id, 0 AS depth
    FROM folders f
    WHERE f.id = sqlc.arg('folder_id') AND f.user_id = sqlc.arg('user_id')
    UNION ALL
    SELECT f.id, s.depth + 1
    FROM folders f
    JOIN subtree s ON f.parent_id = s.id
    WHERE f.user_id = sqlc.arg('user_id')
)
SELECT s.id FROM subtree s ORDER BY s.depth, s.id;

-- name: LockDocumentsInFolders :many
-- The documents a folder delete will remove, with the status each holds
-- now, locked until the marker below is written. The status is read so that
-- a delete whose job could not be queued puts each document back exactly as
-- it was, not as "ready". A document already being deleted is someone
-- else's and is left out.
SELECT id, status FROM documents
WHERE user_id = sqlc.arg('user_id')
  AND folder_id = ANY(sqlc.arg('folder_ids')::bigint[])
  AND status ->> 'state' <> 'deleting'
ORDER BY id
FOR UPDATE;

-- name: MarkDocumentsDeleting :exec
-- The marker: it takes the documents out of search at once and tells the
-- indexer to leave them alone.
UPDATE documents SET status = '{"state": "deleting"}'::jsonb
WHERE user_id = sqlc.arg('user_id')
  AND id = ANY(sqlc.arg('document_ids')::bigint[]);

-- name: RestoreDocumentStatuses :exec
-- The compensation for a delete that could not be queued: each document
-- gets back the status it had, from one object of document id to status.
-- Only a row still carrying the marker is written, so a document something
-- else has moved since is left alone.
UPDATE documents AS d
SET status = restored.status
FROM jsonb_each(sqlc.arg('statuses')::jsonb) AS restored(id, status)
WHERE d.id = restored.id::bigint
  AND d.user_id = sqlc.arg('user_id')
  AND d.status ->> 'state' = 'deleting';

-- name: DocumentsOwned :many
-- The documents among these ids that are this account's, in id order: a
-- move announces each arrival in the order the rows come back, so the order
-- has to be one the rows carry. `document_type` is read as text because the
-- arrival carries the type's name.
SELECT id, folder_id, title, connector_id, status, document_type::text AS document_type
FROM documents
WHERE user_id = sqlc.arg('user_id')
  AND id = ANY(sqlc.arg('document_ids')::bigint[])
ORDER BY id;

-- name: MoveDocuments :exec
-- The one column a move writes. `updated_at` is left alone: where a
-- document is filed is not a change to the document. A folder of another
-- account cannot be named: the composite foreign key (folder_id, user_id)
-- refuses it.
UPDATE documents SET folder_id = sqlc.narg('folder_id')
WHERE user_id = sqlc.arg('user_id')
  AND id = ANY(sqlc.arg('document_ids')::bigint[]);
