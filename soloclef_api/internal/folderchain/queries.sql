-- name: FolderNamed :one
-- The folder of this name under this parent (NULL for the top level), among
-- this account's. The unique constraint uq_folders_owner_parent_name on
-- (user_id, parent_id, name), with NULL parents compared as equal, makes it
-- at most one.
SELECT id FROM folders
WHERE user_id = sqlc.arg('user_id')
  AND name = sqlc.arg('name')
  AND (parent_id = sqlc.narg('parent_id')::bigint
       OR (sqlc.narg('parent_id')::bigint IS NULL AND parent_id IS NULL));
