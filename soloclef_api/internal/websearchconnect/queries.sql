-- An account's own Exa and Tavily keys (web_search_connections). Every
-- statement names the owner, so another account's connection is the same
-- "not connected" as one never made. The sealed key is written here and
-- read by the chat's tools (soloclef_assistant/websearch); no statement of
-- this package selects it.

-- name: ConnectionsOfOwner :many
-- The providers the account has connected, with the mask the screen shows.
SELECT provider, key_mask, updated_at
FROM web_search_connections
WHERE user_id = sqlc.arg(user_id)::uuid
ORDER BY provider;

-- name: Connect :one
-- Connects a provider, or replaces its key: one key per provider per
-- account, and the newest one checked is the one kept.
INSERT INTO web_search_connections (user_id, provider, api_key, key_mask)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(provider), sqlc.arg(api_key), sqlc.arg(key_mask))
ON CONFLICT (user_id, provider) DO UPDATE
SET api_key = EXCLUDED.api_key, key_mask = EXCLUDED.key_mask, updated_at = now()
RETURNING provider, key_mask, updated_at;

-- name: Disconnect :execrows
-- Removes the account's key for a provider. The count is the answer: 0 is
-- "not connected".
DELETE FROM web_search_connections
WHERE user_id = sqlc.arg(user_id)::uuid AND provider = sqlc.arg(provider);
