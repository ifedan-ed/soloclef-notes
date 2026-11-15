-- name: SealedKey :one
-- The account's sealed key for one provider (web_search_connections). Read
-- at the moment of a search and opened for that search alone; never
-- answered. No row is "not connected".
SELECT api_key FROM web_search_connections
WHERE user_id = sqlc.arg(user_id)::uuid
  AND provider = sqlc.arg(provider);
