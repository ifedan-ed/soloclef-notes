-- The "ask first" marks of one account. Every statement names the account,
-- so another account's mark or connection is no row, exactly as one that
-- never existed is no row.

-- name: MarksOfOwner :many
-- Every mark, for the list. A mark is listed by the tool it names, so the
-- order here does not matter.
SELECT connector_id, tool
FROM agent_ask_first_tools
WHERE user_id = sqlc.arg(user_id);

-- name: AppsOfOwner :many
-- The account's connected apps (MCP servers) and the tools each last listed,
-- for the list. Switched-off connections too: a mark is kept while one is
-- off and applies again when it is back on. Only the tool list is read out
-- of the config, which also holds the server's key. Oldest first, the order
-- the chat offers their tools in.
SELECT c.id, c.name,
       COALESCE(c.config -> 'cached_tools' -> 'tools', '[]'::jsonb)::jsonb AS tools
FROM search_source_connectors c
WHERE c.user_id = sqlc.arg(user_id)
  AND c.connector_type = 'MCP_CONNECTOR'
ORDER BY c.id;

-- name: LockAppTool :one
-- The connection, when it is the account's MCP server and last listed the
-- tool (no row otherwise), held until the mark is written so a removal at
-- the same moment cannot leave a mark that names a deleted row. Containment
-- (@>) is false for a list that is not an array, so a broken config lists
-- no tool rather than failing.
SELECT c.id
FROM search_source_connectors c
WHERE c.id = sqlc.arg(connector_id)
  AND c.user_id = sqlc.arg(user_id)
  AND c.connector_type = 'MCP_CONNECTOR'
  AND c.config -> 'cached_tools' -> 'tools' @> jsonb_build_array(jsonb_build_object('name', sqlc.arg(tool)::text))
FOR KEY SHARE OF c;

-- name: MarkTool :exec
-- A second mark writes nothing: the unique constraint compares a NULL
-- connection as equal to itself (NULLS NOT DISTINCT), so the conflict is
-- found.
INSERT INTO agent_ask_first_tools (user_id, connector_id, tool)
VALUES (sqlc.arg(user_id), sqlc.narg(connector_id), sqlc.arg(tool)::text)
ON CONFLICT ON CONSTRAINT uq_agent_ask_first_tools_tool DO NOTHING;

-- name: UnmarkChatTool :exec
-- Removing a mark that is not there writes nothing and is no error: the
-- answer is the same either way.
DELETE FROM agent_ask_first_tools
WHERE user_id = sqlc.arg(user_id)
  AND connector_id IS NULL
  AND tool = sqlc.arg(tool)::text;

-- name: UnmarkAppTool :exec
DELETE FROM agent_ask_first_tools
WHERE user_id = sqlc.arg(user_id)
  AND connector_id = sqlc.arg(connector_id)
  AND tool = sqlc.arg(tool)::text;
