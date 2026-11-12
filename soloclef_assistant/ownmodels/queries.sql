-- An account's own OpenAI-compatible connections, as the chat and the jobs
-- that run on an account's chosen model read them (ownmodels.go).
--
-- Every statement names the caller as the connection's owner. A model has
-- no owner column: it is reached through its connection, and the join names
-- that owner, so a model of another account's connection, or of the
-- operator's (no owner), matches no row whatever ids exist.

-- name: MenuOfAccount :many
-- The models an account may choose from its own connections: switched on,
-- on a connection that is switched on, of the one provider Soloclef calls
-- directly. Ordered by connection and then by name, which is how the menu
-- groups them. Never the key.
SELECT models.id, models.model_id, models.display_name, models.supports_chat,
       models.supports_image_input, models.supports_image_generation,
       connections.base_url
FROM models
JOIN connections ON connections.id = models.connection_id
WHERE connections.user_id = sqlc.arg(user_id)::uuid
  AND connections.provider = 'openai_compatible'
  AND connections.enabled
  AND models.enabled
ORDER BY connections.id, models.model_id;

-- name: EndpointOfModel :one
-- One model of the caller's own connection with what calling it needs: the
-- address, the sealed key and the sealed headers. The one statement of this
-- package that selects the key, for the one thing that uses it, a call.
-- The two switches are answered rather than filtered on, so that a model
-- switched off is told apart from one that is gone.
SELECT models.model_id, models.display_name, models.enabled AS model_enabled,
       connections.enabled AS connection_enabled, connections.base_url,
       connections.api_key AS sealed_key,
       COALESCE(connections.extra ->> 'headers_sealed', '')::text AS sealed_headers
FROM models
JOIN connections ON connections.id = models.connection_id
WHERE models.id = sqlc.arg(id)
  AND connections.user_id = sqlc.arg(user_id)::uuid
  AND connections.provider = 'openai_compatible';

-- name: AllowedHosts :one
-- The hosts the operator lets a person's connection reach although they
-- resolve to an address inside this server's network (a model server on
-- the operator's own machine). No row is no such host.
SELECT value FROM site_settings WHERE key = 'model_connection_allowed_hosts';
