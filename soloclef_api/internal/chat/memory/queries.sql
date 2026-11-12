-- name: LockMemory :one
-- The account's memory document, with its row locked until the transaction
-- ends: the tool compares it with the copy the model was shown and replaces
-- it, and nothing may change it between the two. The column is NOT NULL and
-- '' is the one way to say "nothing remembered".
SELECT memory_md FROM "user" WHERE id = sqlc.arg(user_id) FOR UPDATE;

-- name: SaveMemory :exec
-- updated_at is moved by hand: the schema has no trigger for it.
UPDATE "user" SET memory_md = sqlc.arg(memory_md), updated_at = now() WHERE id = sqlc.arg(user_id);
