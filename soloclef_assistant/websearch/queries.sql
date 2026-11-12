-- name: SearchServerConfigs :many
-- The account's connected MCP servers that are switched on, oldest first: a
-- web search provider's key is the one the person entered when they
-- connected that provider's server, and the oldest matching connection
-- wins. A connector switched off keeps its credentials and is left alone by
-- the chat, so its key is not used. The config is read here and nowhere
-- answered.
SELECT config FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id)
  AND connector_type = 'MCP_CONNECTOR'
  AND enabled
ORDER BY id;
