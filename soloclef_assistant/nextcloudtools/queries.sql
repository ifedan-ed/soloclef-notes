-- An account's Nextcloud connection as the chat's Nextcloud tools read it.
-- Every statement names the owner.

-- name: NextcloudConnectionOfOwner :one
-- The account's Nextcloud connection that is switched on, oldest first:
-- its config holds the sealed app password, and its parser is what a file
-- the chat reads is parsed with (NULL is the deployment's default). A
-- connection switched off is left alone by the chat.
SELECT id, config, parser FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id)
  AND connector_type = 'NEXTCLOUD_CONNECTOR'
  AND enabled
ORDER BY id LIMIT 1;
