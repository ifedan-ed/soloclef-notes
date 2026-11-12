-- name: Servers :many
-- The account's MCP connections that are switched on, oldest first. The
-- order matters: a tool's model-facing name is worked out from this list,
-- and two processes must arrive at the same names from the same rows.
-- Beside each row is the site's list of allowed hosts (one operator row,
-- read in the same statement so a build is one round trip).
SELECT c.id, c.name, c.config,
       (SELECT s.value FROM site_settings s WHERE s.key = 'mcp_allowed_hosts') AS site_hosts
FROM search_source_connectors c
WHERE c.user_id = sqlc.arg(user_id)
  AND c.connector_type = 'MCP_CONNECTOR'
  AND c.enabled
ORDER BY c.id;

-- name: Server :one
-- One connection as it is now, asked at the moment a call is made or a trust
-- decision is taken. The account is part of the question: a connection that
-- was removed, switched off, or was never this account's is "not found",
-- whatever a turn built earlier believes.
SELECT c.name, c.config,
       (SELECT s.value FROM site_settings s WHERE s.key = 'mcp_allowed_hosts') AS site_hosts
FROM search_source_connectors c
WHERE c.id = sqlc.arg(id)
  AND c.user_id = sqlc.arg(user_id)
  AND c.connector_type = 'MCP_CONNECTOR'
  AND c.enabled;

-- name: LockConfig :one
-- The config of a connection its owner is about to change, locked until the
-- transaction ends so that two "always allow" clicks cannot each write a
-- list without the other's name.
SELECT c.config
FROM search_source_connectors c
WHERE c.id = sqlc.arg(id)
  AND c.user_id = sqlc.arg(user_id)
  AND c.connector_type = 'MCP_CONNECTOR'
FOR UPDATE;

-- name: SetConfigKey :execrows
-- Sets one top-level key of a connection's config and leaves the rest.
UPDATE search_source_connectors
SET config = jsonb_set(config, ARRAY[sqlc.arg(key)::text], sqlc.arg(value)::jsonb, true),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
  AND connector_type = 'MCP_CONNECTOR'
  AND jsonb_typeof(config) = 'object';

-- name: MarkAuthExpired :execrows
-- Flags a connection whose server answered 401, so the settings page offers
-- to enter the key again. It changes a row only the first time, which is
-- what tells the caller whether there is anything to announce.
UPDATE search_source_connectors
SET config = jsonb_set(config, '{auth_expired}', 'true'::jsonb, true),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
  AND connector_type = 'MCP_CONNECTOR'
  AND jsonb_typeof(config) = 'object'
  AND config -> 'auth_expired' IS DISTINCT FROM 'true'::jsonb;
