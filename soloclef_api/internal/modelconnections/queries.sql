-- The connections an account made to model providers with its own keys, and
-- the models reachable through them.
--
-- THE KEY IS NOT SELECTED BY THE LIST. `connections.api_key` holds the
-- sealed key (keyseal.go), and the list answers only whether one is saved.
-- So that one fact is asked for -- `has_api_key`: false for NULL and for
-- the empty string, true for anything else -- and the stored text never
-- leaves the database on that path. What is not read cannot be logged,
-- rendered or left in a variable by a later change (modelconnections.go,
-- "The key"). The two statements that do select it are at the end of this
-- file and say so.

-- name: ConnectionsOfAccount :many
-- The caller's own connections, oldest first. The owner is the whole
-- predicate: the operator's connections have no owner (scope 'global',
-- user_id NULL) and so are never in this list, and another account's are
-- another user_id. ix_connections_user_id answers it.
SELECT provider, base_url,
       (api_key IS NOT NULL AND api_key <> '')::boolean AS has_api_key,
       extra, scope, enabled, user_id, id, created_at
FROM connections
WHERE user_id = sqlc.arg(user_id)::uuid
ORDER BY id;

-- name: ModelsOfAccount :many
-- Every model of the caller's own connections, in id order, which is the
-- order they are shown in within a connection. A model has no owner column:
-- it is reached through its connection, and the join names that owner, so
-- this statement cannot return a model of anybody else's connection whatever
-- ids exist. One statement however many connections there are.
SELECT models.connection_id, models.model_id, models.display_name, models.source,
       models.supports_chat, models.max_input_tokens, models.supports_image_input,
       models.supports_tools, models.supports_image_generation,
       models.capabilities_override, models.enabled, models.catalog,
       models.id, models.created_at
FROM models
JOIN connections ON connections.id = models.connection_id
WHERE connections.user_id = sqlc.arg(user_id)::uuid
ORDER BY models.id;

-- ── Writes ───────────────────────────────────────────────────────────────
--
-- Every statement below names the caller as the owner. A connection of
-- another account, and the operator's (no owner), match no row, so an id
-- that is not the caller's is "not found" and never "forbidden": the answer
-- does not say whether the id exists.
--
-- `api_key` here is what the key seal wrote (keyseal.go), never the key as
-- typed.

-- name: CreateConnection :one
INSERT INTO connections (provider, base_url, api_key, extra, scope, enabled, user_id)
VALUES (sqlc.arg(provider), sqlc.narg(base_url), sqlc.narg(sealed_key),
        sqlc.arg(extra)::jsonb, 'user', sqlc.arg(enabled), sqlc.arg(user_id)::uuid)
RETURNING id;

-- name: AddModels :exec
-- Every model picked with a new connection, in one statement however many
-- there are (a provider can list hundreds). The connection is named with
-- its owner, so models cannot be added to a connection that is not the
-- caller's; a model id given twice is stored once.
INSERT INTO models (connection_id, model_id, display_name, source, supports_chat,
                    max_input_tokens, supports_image_input, supports_tools,
                    supports_image_generation, enabled, catalog)
SELECT connections.id, picked.model_id, picked.display_name, picked.source::modelsource,
       picked.supports_chat, picked.max_input_tokens, picked.supports_image_input,
       picked.supports_tools, picked.supports_image_generation, picked.enabled, picked.catalog
FROM jsonb_to_recordset(sqlc.arg(picked)::jsonb) AS picked(
       model_id text, display_name text, source text, supports_chat boolean,
       max_input_tokens integer, supports_image_input boolean, supports_tools boolean,
       supports_image_generation boolean, enabled boolean, catalog jsonb)
JOIN connections ON connections.id = sqlc.arg(connection_id)
                AND connections.user_id = sqlc.arg(user_id)::uuid
ON CONFLICT ON CONSTRAINT uq_models_connection_model_id DO NOTHING;

-- name: UpdateConnection :execrows
-- A field is changed only when the body named it (the set_ flags), so a
-- null that was sent clears a column and a field that was not sent keeps
-- it. `extra` is replaced by what was sent EXCEPT its two credential bags
-- and the connection's sealed headers: none of them is answered
-- (render.go), so the settings screen cannot send them back, and replacing
-- `extra` whole would silently delete a Bedrock or Vertex credential, or a
-- connection's headers, on every save. A bag is changed only by naming it;
-- the headers only through their own field (set_headers), whose object
-- replaces both of their keys (`{}` removes them).
UPDATE connections SET
    provider = CASE WHEN sqlc.arg(set_provider)::boolean THEN sqlc.arg(provider) ELSE provider END,
    base_url = CASE WHEN sqlc.arg(set_base_url)::boolean THEN sqlc.narg(base_url) ELSE base_url END,
    api_key  = CASE WHEN sqlc.arg(set_key)::boolean THEN sqlc.narg(sealed_key) ELSE api_key END,
    extra    = (CASE WHEN sqlc.arg(set_headers)::boolean THEN
                   (CASE WHEN sqlc.arg(set_extra)::boolean THEN
                        COALESCE((SELECT jsonb_object_agg(bag.key, bag.value)
                                  FROM jsonb_each(connections.extra) AS bag
                                  WHERE bag.key IN ('litellm_params', 'kwargs', 'headers_sealed', 'header_names')), '{}'::jsonb)
                        || sqlc.arg(extra)::jsonb
                    ELSE extra END) - 'headers_sealed' - 'header_names' || sqlc.arg(headers)::jsonb
               ELSE
                   (CASE WHEN sqlc.arg(set_extra)::boolean THEN
                        COALESCE((SELECT jsonb_object_agg(bag.key, bag.value)
                                  FROM jsonb_each(connections.extra) AS bag
                                  WHERE bag.key IN ('litellm_params', 'kwargs', 'headers_sealed', 'header_names')), '{}'::jsonb)
                        || sqlc.arg(extra)::jsonb
                    ELSE extra END)
               END),
    enabled  = CASE WHEN sqlc.arg(set_enabled)::boolean THEN sqlc.arg(enabled) ELSE enabled END
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)::uuid;

-- name: DeleteConnection :execrows
-- Its models go with it (ON DELETE CASCADE).
DELETE FROM connections
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)::uuid;

-- name: ConnectionOfAccount :one
-- One connection as the list reads it, for the answer to a write.
SELECT provider, base_url,
       (api_key IS NOT NULL AND api_key <> '')::boolean AS has_api_key,
       extra, scope, enabled, user_id, id, created_at
FROM connections
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)::uuid;

-- name: ModelsOfConnection :many
SELECT models.connection_id, models.model_id, models.display_name, models.source,
       models.supports_chat, models.max_input_tokens, models.supports_image_input,
       models.supports_tools, models.supports_image_generation,
       models.capabilities_override, models.enabled, models.catalog,
       models.id, models.created_at
FROM models
JOIN connections ON connections.id = models.connection_id
WHERE models.connection_id = sqlc.arg(connection_id)
  AND connections.user_id = sqlc.arg(user_id)::uuid
ORDER BY models.id;

-- name: ConnectionForTest :one
-- THE ONE PLACE THE SEALED KEY IS SELECTED, and the bags of `extra` with
-- it: a test sends them to the model proxy, or to an OpenAI-compatible
-- endpoint itself with its sealed headers, which is the only thing they
-- are for. Nothing read here is rendered or logged (test_routes.go).
SELECT provider, base_url, api_key AS sealed_key, extra
FROM connections
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)::uuid;

-- name: ModelForTest :one
-- A stored model with its connection's credentials, by the model's id. A
-- model has no owner column; the join names the owner.
SELECT models.model_id, connections.provider, connections.base_url,
       connections.api_key AS sealed_key, connections.extra
FROM models
JOIN connections ON connections.id = models.connection_id
WHERE models.id = sqlc.arg(id) AND connections.user_id = sqlc.arg(user_id)::uuid;

-- name: AccountIsAdministrator :one
-- Whether the caller administers this deployment: the account's own flag.
-- A connection to any provider but an OpenAI-compatible endpoint names an
-- address the model proxy will call from inside the deployment's network,
-- where this tier's egress guard cannot follow, so naming one is the
-- operator's to do (write_routes.go, "Who may").
SELECT is_superuser FROM "user" WHERE id = sqlc.arg(user_id)::uuid;

-- ── A connection's models: discovery and the edits ───────────────────────
--
-- A model has no owner column. Every statement below reaches it through its
-- connection and names that connection's owner, so a model of another
-- account's connection, and of the operator's (no owner), matches no row.

-- name: ConnectionProvider :one
-- Which provider the caller's own connection is to, and nothing else of it:
-- discovery needs the provider and must not have the key in memory.
SELECT provider FROM connections
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)::uuid;

-- name: ModelProvider :one
-- Which provider a model of the caller's own connection is reached
-- through, and nothing else: who may change the model depends on it
-- (write_routes.go, "Who may").
SELECT connections.provider
FROM models
JOIN connections ON connections.id = models.connection_id
WHERE models.id = sqlc.arg(id) AND connections.user_id = sqlc.arg(user_id)::uuid;

-- name: ModelToVerify :one
-- The model a connection is verified with: an enabled one before a disabled
-- one, a chat model before any other (a chat question is what the test
-- asks), the oldest first. No row when the connection has no model.
SELECT models.model_id
FROM models
JOIN connections ON connections.id = models.connection_id
WHERE models.connection_id = sqlc.arg(connection_id)
  AND connections.user_id = sqlc.arg(user_id)::uuid
ORDER BY models.enabled DESC, (models.supports_chat IS NOT FALSE) DESC, models.id
LIMIT 1;

-- name: StoreDiscoveredModels :exec
-- What the model proxy's catalogue lists for the connection's provider, in
-- one statement however many there are. A model not seen before is stored
-- switched off: listing a model is not choosing it. One already stored
-- keeps its name, its source, its switch and its input limit (a limit a
-- person set outlives every refresh), and takes a capability only where
-- the catalogue states one, so a refresh never blanks a fact with "unknown".
INSERT INTO models (connection_id, model_id, source, supports_chat, max_input_tokens,
                    supports_image_input, supports_tools, supports_image_generation,
                    enabled, catalog)
SELECT connections.id, listed.model_id, 'DISCOVERED', listed.supports_chat,
       listed.max_input_tokens, listed.supports_image_input, listed.supports_tools,
       listed.supports_image_generation, false, listed.catalog
FROM jsonb_to_recordset(sqlc.arg(listed)::jsonb) AS listed(
       model_id text, supports_chat boolean, max_input_tokens integer,
       supports_image_input boolean, supports_tools boolean,
       supports_image_generation boolean, catalog jsonb)
JOIN connections ON connections.id = sqlc.arg(connection_id)
                AND connections.user_id = sqlc.arg(user_id)::uuid
ON CONFLICT ON CONSTRAINT uq_models_connection_model_id DO UPDATE SET
    supports_chat             = COALESCE(EXCLUDED.supports_chat, models.supports_chat),
    supports_image_input      = COALESCE(EXCLUDED.supports_image_input, models.supports_image_input),
    supports_tools            = COALESCE(EXCLUDED.supports_tools, models.supports_tools),
    supports_image_generation = COALESCE(EXCLUDED.supports_image_generation, models.supports_image_generation),
    max_input_tokens          = COALESCE(models.max_input_tokens, EXCLUDED.max_input_tokens),
    catalog                   = EXCLUDED.catalog;

-- name: SetModelsEnabled :many
-- Switch several models of one connection on or off. An id that is not a
-- model of this connection, or whose connection is not the caller's, is
-- simply not among the rows changed.
UPDATE models SET enabled = sqlc.arg(enabled)
FROM connections
WHERE connections.id = models.connection_id
  AND models.connection_id = sqlc.arg(connection_id)
  AND connections.user_id = sqlc.arg(user_id)::uuid
  AND models.id = ANY(sqlc.arg(model_ids)::bigint[])
RETURNING models.connection_id, models.model_id, models.display_name, models.source,
          models.supports_chat, models.max_input_tokens, models.supports_image_input,
          models.supports_tools, models.supports_image_generation,
          models.capabilities_override, models.enabled, models.catalog,
          models.id, models.created_at;

-- name: UpdateModel :one
-- A field is changed only when the body named it (the set_ flags), so a
-- null that was sent clears a column and a field left out keeps it.
UPDATE models SET
    display_name = CASE WHEN sqlc.arg(set_display_name)::boolean THEN sqlc.narg(display_name) ELSE models.display_name END,
    enabled = CASE WHEN sqlc.arg(set_enabled)::boolean THEN sqlc.arg(enabled)::boolean ELSE models.enabled END,
    supports_chat = CASE WHEN sqlc.arg(set_supports_chat)::boolean THEN sqlc.narg(supports_chat)::boolean ELSE models.supports_chat END,
    max_input_tokens = CASE WHEN sqlc.arg(set_max_input_tokens)::boolean THEN sqlc.narg(max_input_tokens)::integer ELSE models.max_input_tokens END,
    supports_image_input = CASE WHEN sqlc.arg(set_supports_image_input)::boolean THEN sqlc.narg(supports_image_input)::boolean ELSE models.supports_image_input END,
    supports_tools = CASE WHEN sqlc.arg(set_supports_tools)::boolean THEN sqlc.narg(supports_tools)::boolean ELSE models.supports_tools END,
    supports_image_generation = CASE WHEN sqlc.arg(set_supports_image_generation)::boolean THEN sqlc.narg(supports_image_generation)::boolean ELSE models.supports_image_generation END,
    capabilities_override = CASE WHEN sqlc.arg(set_capabilities_override)::boolean THEN sqlc.arg(capabilities_override)::jsonb ELSE models.capabilities_override END
FROM connections
WHERE connections.id = models.connection_id
  AND models.id = sqlc.arg(id)
  AND connections.user_id = sqlc.arg(user_id)::uuid
RETURNING models.connection_id, models.model_id, models.display_name, models.source,
          models.supports_chat, models.max_input_tokens, models.supports_image_input,
          models.supports_tools, models.supports_image_generation,
          models.capabilities_override, models.enabled, models.catalog,
          models.id, models.created_at;

-- ── The site's own connections ───────────────────────────────────────────
--
-- The operator's connections (scope 'global', no owner) as every signed-in
-- account may see them: that they exist, to which provider, and their
-- models. NOT their address, NOT `extra`, and never the key: those are the
-- operator's, and nothing an account does with this list needs them.

-- name: GlobalConnections :many
SELECT provider,
       (api_key IS NOT NULL AND api_key <> '')::boolean AS has_api_key,
       enabled, id, created_at
FROM connections
WHERE scope = 'global' AND user_id IS NULL
ORDER BY id;

-- name: GlobalModels :many
SELECT models.connection_id, models.model_id, models.display_name, models.source,
       models.supports_chat, models.max_input_tokens, models.supports_image_input,
       models.supports_tools, models.supports_image_generation,
       models.capabilities_override, models.enabled, models.catalog,
       models.id, models.created_at
FROM models
JOIN connections ON connections.id = models.connection_id
WHERE connections.scope = 'global' AND connections.user_id IS NULL
ORDER BY models.id;

-- name: GlobalConnectionExists :one
-- Whether the operator has given the site a connection that is switched on.
SELECT EXISTS (
    SELECT 1 FROM connections
    WHERE scope = 'global' AND user_id IS NULL AND enabled
)::boolean AS present;
