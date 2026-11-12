-- The statements of a notebook's sources. Every one names the owner, and the
-- schema's composite keys refuse a source row that would point at another
-- account's notebook, document or folder, so the owner predicate is both the
-- access check and a second lock on the same door.

-- name: TouchNotebook :execrows
-- The caller's notebook, marked as used now: the notebook list is ordered by
-- it. Its row count is also how a route learns the notebook is the caller's.
UPDATE notebooks SET updated_at = sqlc.arg(used_at)
WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- name: NotebookIsOwned :one
SELECT EXISTS (
    SELECT 1 FROM notebooks WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id)
);

-- name: DocumentSources :many
-- The notebook's document sources, with the document's title, type and
-- state. The route merges them with the folder sources in the order they
-- were added (sources.go).
SELECT s.id, s.document_id, d.title, d.document_type::text AS document_type,
       (d.status ->> 'state')::text AS status, s.enabled, s.added_at
FROM notebook_sources s
JOIN documents d ON d.id = s.document_id AND d.user_id = s.user_id
WHERE s.notebook_id = sqlc.arg(notebook_id) AND s.user_id = sqlc.arg(user_id)
ORDER BY s.added_at, s.id;

-- name: FolderSources :many
SELECT s.id, s.folder_id, f.name, s.enabled, s.added_at
FROM notebook_sources s
JOIN folders f ON f.id = s.folder_id AND f.user_id = s.user_id
WHERE s.notebook_id = sqlc.arg(notebook_id) AND s.user_id = sqlc.arg(user_id)
ORDER BY s.added_at, s.id;

-- name: OwnedDocumentCount :one
SELECT count(*) FROM documents
WHERE id = ANY(sqlc.arg(ids)::bigint[]) AND user_id = sqlc.arg(user_id);

-- name: OwnedFolderCount :one
SELECT count(*) FROM folders
WHERE id = ANY(sqlc.arg(ids)::bigint[]) AND user_id = sqlc.arg(user_id);

-- name: LinkDocuments :exec
-- Adding a document that is already a source is no change.
INSERT INTO notebook_sources (notebook_id, user_id, document_id, added_at)
SELECT sqlc.arg(notebook_id), sqlc.arg(user_id), id, sqlc.arg(added_at)
FROM unnest(sqlc.arg(ids)::bigint[]) AS id
ON CONFLICT (notebook_id, document_id) DO NOTHING;

-- name: LinkFolders :exec
INSERT INTO notebook_sources (notebook_id, user_id, folder_id, added_at)
SELECT sqlc.arg(notebook_id), sqlc.arg(user_id), id, sqlc.arg(added_at)
FROM unnest(sqlc.arg(ids)::bigint[]) AS id
ON CONFLICT (notebook_id, folder_id) DO NOTHING;

-- name: SetSourceEnabled :execrows
UPDATE notebook_sources SET enabled = sqlc.arg(enabled)
WHERE id = sqlc.arg(id) AND notebook_id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- name: SetSourcesEnabled :exec
-- Every source of the notebook, or the listed ones when `listed` is true.
UPDATE notebook_sources SET enabled = sqlc.arg(enabled)
WHERE notebook_id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id)
  AND (NOT sqlc.arg(listed)::boolean OR id = ANY(sqlc.arg(ids)::bigint[]));

-- name: UnlinkSource :execrows
-- The document or folder itself is untouched.
DELETE FROM notebook_sources
WHERE id = sqlc.arg(id) AND notebook_id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- Whether adding these documents owes the notebook a name: it still names
-- itself, has runs left (two in all, the worker's notebookname.maxRuns),
-- and at least one of them is ready to be read. A document still being
-- read asks for the name itself when the indexer finishes it
-- (soloclef_worker/internal/namerequest).
-- name: NamingOwed :one
SELECT EXISTS (
    SELECT 1 FROM notebooks AS n
    WHERE n.id = sqlc.arg(notebook_id) AND n.user_id = sqlc.arg(user_id)
      AND n.title_source = 'auto'
      AND CASE WHEN jsonb_typeof(n.auto_title -> 'runs') = 'number'
               THEN (n.auto_title ->> 'runs')::numeric ELSE 0 END < 2
      AND EXISTS (
          SELECT 1 FROM documents AS d
          WHERE d.id = ANY(sqlc.arg(ids)::bigint[]) AND d.user_id = n.user_id
            AND d.status ->> 'state' = 'ready'))::boolean;
