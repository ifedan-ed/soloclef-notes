-- name: UserMemory :one
-- app/services/memory/service.py:_load_target for the user scope, then
-- _get_memory. The column is nullable with a '' default, and the service reads
-- `getattr(target, "memory_md", None) or ""`, so NULL and '' are the same
-- answer.
SELECT COALESCE(memory_md, '') AS memory_md FROM "user" WHERE id = $1;

-- name: SaveUserMemory :exec
-- The write. The Python service assigns the attribute and commits; nothing
-- else about the row changes.
UPDATE "user" SET memory_md = $2 WHERE id = $1;

-- name: TeamMemory :one
-- The same for the team scope, over workspaces.shared_memory_md.
SELECT COALESCE(shared_memory_md, '') AS memory_md FROM workspaces WHERE id = $1;

-- name: SaveTeamMemory :exec
UPDATE workspaces SET shared_memory_md = $2 WHERE id = $1;
