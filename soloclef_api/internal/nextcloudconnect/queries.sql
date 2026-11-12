-- An account's Nextcloud connection: one row, the account's alone. Every
-- statement names the owner.

-- name: LockAccount :one
-- Taken first by the write that saves a sign-in, so that two sign-ins of
-- one account finishing at the same moment queue, and the second replaces
-- the first's row instead of adding one beside it.
SELECT id FROM "user" WHERE id = sqlc.arg(user_id) FOR UPDATE;

-- name: ConnectionOfOwner :one
-- The account's Nextcloud connection. One is all the sign-in ever makes;
-- the lowest id is taken so that the answer is the same every time if an
-- older deployment left two.
SELECT id, config FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id) AND connector_type = 'NEXTCLOUD_CONNECTOR'
ORDER BY id LIMIT 1;

-- name: ReplaceLogin :execrows
-- Connecting again replaces the credentials in the same row, so imported
-- files stay linked to it, and switches the connection back on: signing in
-- is the person saying they want it used.
UPDATE search_source_connectors
SET config = sqlc.arg(config)::jsonb, enabled = true, updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'NEXTCLOUD_CONNECTOR';

-- name: InsertConnection :one
INSERT INTO search_source_connectors (user_id, name, connector_type, is_indexable, config)
VALUES (sqlc.arg(user_id), sqlc.arg(name), 'NEXTCLOUD_CONNECTOR', false, sqlc.arg(config)::jsonb)
RETURNING id;

-- name: DeleteConnection :one
-- Disconnecting: the row goes and its config comes back, once, so the app
-- password in it can be ended at Nextcloud. The documents imported through
-- it stay (their link to the connection is ON DELETE SET NULL): they are
-- the account's own documents now. No row means the connection is not the
-- caller's, is not a Nextcloud one, or is not there.
DELETE FROM search_source_connectors
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'NEXTCLOUD_CONNECTOR'
RETURNING config;
