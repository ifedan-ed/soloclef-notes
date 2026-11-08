-- name: TokensOfAccount :many
-- app/routes/personal_access_tokens_routes.py:list_personal_access_tokens.
-- The ordering is the Python route's own, so it is reproduced rather than left
-- to Postgres: newest first.
SELECT id,
       label,
       token_prefix,
       expires_at,
       last_used_at,
       created_at
FROM personal_access_tokens
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: RevokeToken :exec
-- delete_personal_access_token. The account is part of the predicate, not a
-- check beforehand: one person's id names another person's key and the delete
-- matches nothing, which is why the route answers 204 either way and is not an
-- oracle for whose keys exist.
DELETE FROM personal_access_tokens
WHERE id = $1 AND user_id = $2;
