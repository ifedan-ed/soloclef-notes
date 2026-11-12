-- An account's Google Drive connection: one row, the account's alone.
-- Every statement names the owner. The callback, which has no session,
-- names the account its sealed cookie holds.

-- name: LockAccount :one
-- Taken first by the write that saves a grant, so that two consents of one
-- account finishing at the same moment queue, and the second replaces the
-- first's row instead of adding one beside it.
SELECT id FROM "user" WHERE id = sqlc.arg(user_id) FOR UPDATE;

-- name: ConnectionOfOwner :one
-- The account's Drive connection: the lowest id, so the answer is the same
-- every time if an older deployment left two.
SELECT id, config FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id) AND connector_type = 'GOOGLE_DRIVE_CONNECTOR'
ORDER BY id LIMIT 1;

-- name: ConnectionOfOwnerByID :one
SELECT id, config FROM search_source_connectors
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'GOOGLE_DRIVE_CONNECTOR';

-- name: ReplaceGrant :execrows
-- Connecting again replaces the grant in the same row, so imported files
-- stay linked to it, and switches the connection back on.
UPDATE search_source_connectors
SET config = sqlc.arg(config)::jsonb, enabled = true, updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'GOOGLE_DRIVE_CONNECTOR';

-- name: InsertConnection :one
-- A new connection is indexable and re-reads its picked files on a
-- schedule from the start.
INSERT INTO search_source_connectors
    (user_id, name, connector_type, is_indexable, config,
     periodic_indexing_enabled, indexing_frequency_minutes, next_scheduled_at)
VALUES (sqlc.arg(user_id), sqlc.arg(name), 'GOOGLE_DRIVE_CONNECTOR', true, sqlc.arg(config)::jsonb,
        true, sqlc.arg(indexing_frequency_minutes), sqlc.arg(next_scheduled_at))
RETURNING id;

-- name: MarkExpired :exec
-- Google refused the stored grant: the settings page then offers to
-- connect again. Only the one key is written.
UPDATE search_source_connectors
SET config = jsonb_set(config, '{auth_expired}', 'true'::jsonb, true), updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'GOOGLE_DRIVE_CONNECTOR'
  AND jsonb_typeof(config) = 'object';

-- name: DeleteConnection :one
-- Disconnecting: the row goes and its config comes back, once, so the
-- grant in it can be ended at Google. Imported documents stay (their link
-- is ON DELETE SET NULL).
DELETE FROM search_source_connectors
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'GOOGLE_DRIVE_CONNECTOR'
RETURNING config;
