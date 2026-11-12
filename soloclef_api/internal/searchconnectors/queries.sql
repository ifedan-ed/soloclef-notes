-- An account's connections: its Drive, its Nextcloud, its mailboxes, its MCP
-- servers. A connection belongs to the account whose credentials it holds,
-- and EVERY statement here names that owner. A connection of another account
-- is the same "not found" as one that was never made, so no route says
-- whether an id is in use.
--
-- The two lists are ordered by name, then id. By name, because the page is a
-- settings list a person scans for "the Nextcloud one", and the web app shows
-- the rows in the order it is sent. `lower(name)` so that "github" and
-- "GitHub" sit together; `id` last, because the order has to be total for a
-- page to mean anything.

-- name: ConnectionOfOwner :one
SELECT id, name, connector_type, enabled, is_indexable, last_indexed_at, config,
       enable_vision_llm, periodic_indexing_enabled, indexing_frequency_minutes,
       next_scheduled_at, user_id, created_at, updated_at
FROM search_source_connectors
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: ServerOfOwner :one
-- The MCP routes' read: the row only if it is an MCP server, so a connection
-- of another type is "MCP connector not found".
SELECT id, name, connector_type, enabled, is_indexable, last_indexed_at, config,
       enable_vision_llm, periodic_indexing_enabled, indexing_frequency_minutes,
       next_scheduled_at, user_id, created_at, updated_at
FROM search_source_connectors
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'MCP_CONNECTOR';

-- name: ConnectionsOfOwner :many
-- One page of the account's connections, of every type, switched on or off:
-- the switch has to be able to see one to turn it back on. The page is cut
-- here and not in the route, so a page costs the rows it shows.
SELECT id, name, connector_type, enabled, is_indexable, last_indexed_at, config,
       enable_vision_llm, periodic_indexing_enabled, indexing_frequency_minutes,
       next_scheduled_at, user_id, created_at, updated_at
FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id)
ORDER BY lower(name), id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(skip);

-- name: ServersOfOwner :many
-- The account's MCP servers, in the same order as the list above so the two
-- pages agree.
SELECT id, name, connector_type, enabled, is_indexable, last_indexed_at, config,
       enable_vision_llm, periodic_indexing_enabled, indexing_frequency_minutes,
       next_scheduled_at, user_id, created_at, updated_at
FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id) AND connector_type = 'MCP_CONNECTOR'
ORDER BY lower(name), id;

-- name: SetConnectionEnabled :one
-- The switch. Off keeps the connection and its credentials; the chat and the
-- indexing schedule then leave it alone (both read this column). No row back
-- means the connection is not the caller's or is not there.
UPDATE search_source_connectors
SET enabled = sqlc.arg(enabled), updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING id, name, connector_type, enabled, is_indexable, last_indexed_at, config,
          enable_vision_llm, periodic_indexing_enabled, indexing_frequency_minutes,
          next_scheduled_at, user_id, created_at, updated_at;

-- name: LockTrustedTools :one
-- The trusted-tool list of an MCP server its owner is about to edit, locked
-- until the transaction ends, so two edits made at once cannot each write a
-- list without the other's change. The list alone is read: the rest of the
-- config holds the server's key and this edit has no use for it.
SELECT COALESCE(config -> 'trusted_tools', 'null'::jsonb)::jsonb AS trusted_tools
FROM search_source_connectors
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'MCP_CONNECTOR'
FOR UPDATE;

-- name: StoreTrustedTools :execrows
-- Writes the one key and leaves the rest of the config as it is. A config
-- that is not an object cannot take a key, matches nothing, and the route
-- reports the server as one it cannot edit.
UPDATE search_source_connectors
SET config = jsonb_set(config, '{trusted_tools}', sqlc.arg(trusted_tools)::jsonb, true),
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'MCP_CONNECTOR'
  AND jsonb_typeof(config) = 'object';

-- name: DeleteServer :execrows
-- An MCP server holds no documents, so its row is the whole of it. The count
-- is the answer: 0 is "not found".
DELETE FROM search_source_connectors
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'MCP_CONNECTOR';

-- name: MarkConnectionDocumentsDeleting :execrows
-- The first step of removing a connection: every document that came through
-- it is marked "deleting" in one statement, which takes them all out of
-- search at once and tells the indexing worker to leave them alone, before
-- any of them is removed. A removal that is cut short therefore leaves
-- documents that are hidden, not documents that are half there, and asking
-- again carries on. Documents already marked are left as they are.
UPDATE documents SET status = '{"state": "deleting"}'::jsonb
WHERE connector_id = sqlc.arg(connector_id)
  AND user_id = sqlc.arg(user_id)
  AND COALESCE(status ->> 'state', '') <> 'deleting';

-- name: ConnectionDocumentIDs :many
-- One batch of the connection's remaining documents, lowest id first. Each
-- is removed (and its row with it) before the next batch is asked for, so
-- the same statement finds the next ones and the memory of a removal does
-- not grow with the connection.
SELECT id FROM documents
WHERE connector_id = sqlc.arg(connector_id) AND user_id = sqlc.arg(user_id)
ORDER BY id LIMIT sqlc.arg(batch);

-- name: DeleteConnection :execrows
-- The connection's row, last. A document indexed through it after its
-- documents were removed keeps its row and loses the link (ON DELETE SET
-- NULL). Zero rows means another request removed it first.
DELETE FROM search_source_connectors
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: OwnerIsAdministrator :one
-- Whether the caller administers the site, which is the account's own
-- `is_superuser` flag and nothing else. An administrator's MCP server is a
-- site server, held to the site's list of allowed hosts; anyone else's is
-- their own.
SELECT is_superuser FROM "user" WHERE id = sqlc.arg(user_id);

-- name: SiteAllowedHosts :one
-- The hosts a site server may be on, as the operator saved them. No row
-- means none was saved, and the deployment's configured list applies.
SELECT value FROM site_settings WHERE key = 'mcp_allowed_hosts';

-- name: InsertConnection :one
-- A new connection of the caller's. The account's name for a connection is
-- unique within its type (uq_search_source_connectors_owner_type_name), and
-- a second one of the same name inserts nothing and returns no row: the
-- route decides what that means, without a check before the insert that
-- another request could get between.
INSERT INTO search_source_connectors
    (user_id, name, connector_type, is_indexable, config, enable_vision_llm,
     periodic_indexing_enabled, indexing_frequency_minutes, next_scheduled_at)
VALUES (sqlc.arg(user_id), sqlc.arg(name), sqlc.arg(connector_type), sqlc.arg(is_indexable),
        sqlc.arg(config)::jsonb, sqlc.arg(enable_vision_llm),
        sqlc.arg(periodic_indexing_enabled), sqlc.narg(indexing_frequency_minutes),
        sqlc.narg(next_scheduled_at))
ON CONFLICT (user_id, connector_type, name) DO NOTHING
RETURNING id, name, connector_type, enabled, is_indexable, last_indexed_at, config,
          enable_vision_llm, periodic_indexing_enabled, indexing_frequency_minutes,
          next_scheduled_at, user_id, created_at, updated_at;

-- name: ConnectionOfOwnerByName :one
-- The connection an insert found already there.
SELECT id, name, connector_type, enabled, is_indexable, last_indexed_at, config,
       enable_vision_llm, periodic_indexing_enabled, indexing_frequency_minutes,
       next_scheduled_at, user_id, created_at, updated_at
FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id) AND connector_type = sqlc.arg(connector_type)
  AND name = sqlc.arg(name);

-- name: UpdateServer :one
-- An edit of an MCP server: its name, its settings, or both. Only the
-- `server_config` key is written, so the trusted tools and the origin stay
-- whatever they are at the moment of the write, not what they were when
-- the form was opened. New settings also drop the two things that were
-- true of the old ones only: the "enter the key again" flag, and the tool
-- list the old address answered with.
UPDATE search_source_connectors
SET name = COALESCE(sqlc.narg(name), name),
    config = CASE WHEN sqlc.narg(server_config)::jsonb IS NULL THEN config
                  ELSE jsonb_set(config - 'auth_expired' - 'cached_tools', '{server_config}',
                                 sqlc.narg(server_config)::jsonb, true) END,
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'MCP_CONNECTOR'
  AND jsonb_typeof(config) = 'object'
RETURNING id, name, connector_type, enabled, is_indexable, last_indexed_at, config,
          enable_vision_llm, periodic_indexing_enabled, indexing_frequency_minutes,
          next_scheduled_at, user_id, created_at, updated_at;

-- name: UpdateConnection :one
-- An edit of a connection that is not an MCP server. Each column is written
-- only when the request named it. `settings` is merged key by key over the
-- stored config, so the keys the request cannot name (the grant, the
-- password) are never read out and written back: they are simply not
-- touched. The schedule is the server's: the route sends the next run when
-- the edit turns the schedule on or changes its pace, and clears it when
-- the edit turns it off.
UPDATE search_source_connectors
SET name = COALESCE(sqlc.narg(name), name),
    config = CASE WHEN sqlc.narg(settings)::jsonb IS NULL THEN config
                  ELSE config || sqlc.narg(settings)::jsonb END,
    enable_vision_llm = COALESCE(sqlc.narg(enable_vision_llm), enable_vision_llm),
    periodic_indexing_enabled = sqlc.arg(periodic_indexing_enabled),
    indexing_frequency_minutes = sqlc.narg(indexing_frequency_minutes),
    next_scheduled_at = sqlc.narg(next_scheduled_at),
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type <> 'MCP_CONNECTOR'
  AND jsonb_typeof(config) = 'object'
RETURNING id, name, connector_type, enabled, is_indexable, last_indexed_at, config,
          enable_vision_llm, periodic_indexing_enabled, indexing_frequency_minutes,
          next_scheduled_at, user_id, created_at, updated_at;
