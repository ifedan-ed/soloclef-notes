-- name: FolderByID :one
-- `session.get(Folder, folder_id)`, which is what every route but the list and
-- the create begins with. The predicate is deliberately **unscoped**: all five
-- of those routes load the row before asking whether the caller may see it, so
-- a folder of another workspace is a 403 and an absent one is a 404. That is a
-- folder-existence oracle and it is the Python routes' own shape
-- (custom/docs/go-api-tier.md, "defects the port exposed"); scoping it here
-- would answer 404 where that tier answers 403.
SELECT id, name, position, parent_id, workspace_id, created_by_id,
       created_at, updated_at, metadata
FROM folders WHERE id = $1;

-- name: FolderAncestors :many
-- get_folder_breadcrumb's own recursive CTE, word for word, including the
-- ordering by depth so the root comes first.
--
-- move_folder's check_no_circular_reference asks the *same* chain, in its own
-- copy of this CTE, and then only whether one id is in it. This statement
-- answers that too, so the slice runs one query where the Python route has two
-- spellings of it: the extra name column and the ordering cannot change a
-- set-membership answer, and the self row the chain starts with is the case
-- move_folder has already refused by then with its own equality check.
WITH RECURSIVE ancestors AS (
    SELECT f.id, f.name, f.parent_id, 0 AS depth
    FROM folders f WHERE f.id = $1
    UNION ALL
    SELECT f.id, f.name, f.parent_id, a.depth + 1
    FROM folders f JOIN ancestors a ON f.id = a.parent_id
)
SELECT a.id, a.name FROM ancestors a ORDER BY a.depth DESC;

-- name: FolderDepth :one
-- app/services/folder_service.py:get_folder_depth. A root folder is 1.
WITH RECURSIVE ancestors AS (
    SELECT f.id, f.parent_id, 1 AS depth
    FROM folders f
    WHERE f.id = $1
    UNION ALL
    SELECT f.id, f.parent_id, a.depth + 1
    FROM folders f
    JOIN ancestors a ON f.id = a.parent_id
)
SELECT COALESCE(MAX(a.depth), 0)::int AS depth FROM ancestors a;

-- name: SubtreeMaxDepth :one
-- get_subtree_max_depth. -1 means the folder has no children at all, which the
-- caller turns into a subtree depth of 0.
WITH RECURSIVE descendants AS (
    SELECT f.id, 0 AS depth
    FROM folders f
    WHERE f.parent_id = $1
    UNION ALL
    SELECT f.id, d.depth + 1
    FROM folders f
    JOIN descendants d ON f.parent_id = d.id
)
SELECT COALESCE(MAX(d.depth), -1)::int AS depth FROM descendants d;

-- name: LastSiblingPosition :one
-- generate_folder_position's own query for the append case. The ordering is the
-- column's collation on both tiers, which is why it is the same statement and
-- not a sort in Go.
SELECT position FROM folders
WHERE workspace_id = sqlc.arg('workspace_id')
  AND (parent_id = sqlc.narg('parent_id')
       OR (sqlc.narg('parent_id') IS NULL AND parent_id IS NULL))
ORDER BY position DESC LIMIT 1;

-- name: InsertFolder :one
-- create_folder's insert. created_at and updated_at are Python-side defaults
-- evaluated in the application rather than column defaults, so they are passed
-- in; the Python model declares updated_at before the created_at its mixin
-- adds, so that tier's two clock readings come in that order and differ by a
-- few microseconds.
INSERT INTO folders (name, position, parent_id, workspace_id, created_by_id,
                     created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, name, position, parent_id, workspace_id, created_by_id,
          created_at, updated_at, metadata;

-- name: RenameFolder :exec
-- update_folder's flush. The route only runs it when the name actually
-- changes, because SQLAlchemy emits no UPDATE for an attribute reassigned to
-- an equal value — and so leaves updated_at alone, which a row diff sees.
UPDATE folders SET name = $2, updated_at = $3 WHERE id = $1;

-- name: ReparentFolder :exec
-- move_folder's flush: the new parent and the position generated for it.
UPDATE folders SET parent_id = $2, position = $3, updated_at = $4 WHERE id = $1;

-- name: RepositionFolder :exec
-- reorder_folder's flush.
UPDATE folders SET position = $2, updated_at = $3 WHERE id = $1;

-- name: SetFolderMetadata :exec
-- stop_watching_folder's flush. The value is the whole column rewritten rather
-- than a jsonb_set, because that is what `folder.folder_metadata = {**m,
-- "watched": False}` stores: the dict goes back out through json.dumps, so a
-- number the column held more precisely than a float64 comes back rounded
-- (internal/pythonjson.MarshalColumn). jsonb_set would have kept it, which
-- would have been a different row.
UPDATE folders SET metadata = $2::jsonb, updated_at = $3 WHERE id = $1;

-- name: FolderSubtreeIDs :many
-- delete_folder's get_folder_subtree_ids, inclusive of the folder itself. The
-- order is the worktable's and is in the enqueued message, so it is this
-- statement and not a sort in Go.
WITH RECURSIVE subtree AS (
    SELECT f.id FROM folders f WHERE f.id = $1
    UNION ALL
    SELECT f.id FROM folders f JOIN subtree s ON f.parent_id = s.id
)
SELECT s.id FROM subtree s;

-- name: DocumentsInFoldersNotDeleting :many
-- delete_folder's document read. `Document.status["state"].as_string() !=
-- "deleting"` is `status ->> 'state' <> 'deleting'`, and that is NULL — so the
-- row is **excluded** — for a document whose status has no state at all. A
-- document with `{}` is therefore left alone and not queued, which was checked
-- against the running tier rather than reasoned about.
SELECT id FROM documents
WHERE folder_id = ANY(sqlc.arg('folder_ids')::int[])
  AND status ->> 'state' <> 'deleting';

-- name: MarkDocumentsDeleting :exec
-- The marker, over the ids just read.
UPDATE documents SET status = '{"state": "deleting"}'::jsonb
WHERE id = ANY(sqlc.arg('document_ids')::int[]);

-- name: MarkDocumentsReady :exec
-- The compensation for an enqueue that did not happen. It writes "ready" over
-- whatever each state was, exactly as the Python route's own revert does.
UPDATE documents SET status = '{"state": "ready"}'::jsonb
WHERE id = ANY(sqlc.arg('document_ids')::int[]);
