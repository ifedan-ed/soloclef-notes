-- name: UserMemory :one
-- The account's memory document. The column is NOT NULL with a '' default:
-- there is one way to say "nothing remembered", so nothing is coalesced.
SELECT memory_md FROM "user" WHERE id = $1;

-- name: SaveUserMemory :exec
-- The write. Nothing else about the row changes.
UPDATE "user" SET memory_md = $2 WHERE id = $1;
