-- name: TokensOfAccount :many
-- app/routes/personal_access_tokens_routes.py:list_personal_access_tokens.
-- The ordering is the Python route's own, so it is reproduced rather than left
-- to Postgres: newest first, with `id` breaking the tie.
--
-- The tie-break was added on both tiers on 2026-11-09. created_at is not
-- unique, so the Python route's ORDER BY left the tied rows to the plan --
-- which Postgres changes on a prepared statement's fifth execution per
-- connection, turning an index scan into an unstable sort. One tier could
-- therefore serve one account two different orders, and the two tiers could
-- disagree while each was internally consistent.
SELECT id,
       label,
       token_prefix,
       expires_at,
       last_used_at,
       created_at
FROM personal_access_tokens
WHERE user_id = $1
ORDER BY created_at DESC, id DESC;

-- name: RevokeToken :exec
-- delete_personal_access_token. The account is part of the predicate, not a
-- check beforehand: one person's id names another person's key and the delete
-- matches nothing, which is why the route answers 204 either way and is not an
-- oracle for whose keys exist.
DELETE FROM personal_access_tokens
WHERE id = $1 AND user_id = $2;

-- name: InsertToken :one
-- A new key: its owner, the hash of the key and the short prefix the list
-- shows, never the key itself (create_route.go). created_at is the
-- database's, so a key minted in a transaction is stamped with its time.
INSERT INTO personal_access_tokens (user_id, token_hash, token_prefix, label, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, expires_at;
