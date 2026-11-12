-- An account's Google Drive connection as the chat's Drive tools read it.
-- Every statement names the owner.

-- name: DriveConnectionOfOwner :one
-- The account's Drive connection that is switched on, oldest first: its
-- config holds the grant, and its parser is what a PDF or an Office file
-- read from Drive is read with (NULL: the deployment's default). A
-- connection switched off keeps its grant and is left alone by the chat.
SELECT id, config, parser FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id)
  AND connector_type = 'GOOGLE_DRIVE_CONNECTOR'
  AND enabled
ORDER BY id LIMIT 1;

-- name: MergeDriveConfig :execrows
-- Merges keys over the connection's config, inside the statement: a
-- renewed access token, or the flag that says to connect again. Nothing
-- else on the row (the refresh token, the picks, the feed position) is
-- read out and written back.
UPDATE search_source_connectors
SET config = config || sqlc.arg(patch)::jsonb, updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'GOOGLE_DRIVE_CONNECTOR';
