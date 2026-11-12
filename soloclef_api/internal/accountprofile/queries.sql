-- name: AccountProfile :one
-- The caller's own account, as GET /users/me shows it. The gate loads only
-- the caller's id (internal/principal), so the profile is read here.
--
-- `hashed_password` is deliberately not selected: a column never read is a
-- column that cannot leak.
SELECT id,
       email,
       is_active,
       is_superuser,
       is_verified,
       display_name,
       avatar_url
FROM "user"
WHERE id = $1;

-- name: UpdateAccountProfile :one
-- `update_current_user_profile`, which is fastapi-users'
-- `user_db.update(user, update_dict)` for the two columns UserUpdate declares.
--
-- A CASE per column rather than a COALESCE, as the role, log and prompt updates
-- are: `create_update_dict()` is `model_dump(exclude_unset=True)`, so a field
-- the request did not mention is not written at all while a field mentioned as
-- null writes NULL — and both columns are nullable, so the null is a value
-- here and not the 500 it is on the role.
--
-- A body that mentions nothing skips this statement entirely, because the
-- Python session then has no changed attribute to flush and emits no UPDATE.
UPDATE "user"
SET display_name = CASE WHEN sqlc.arg(set_display_name)::boolean THEN sqlc.narg(display_name)::varchar ELSE display_name END,
    avatar_url   = CASE WHEN sqlc.arg(set_avatar_url)::boolean THEN sqlc.narg(avatar_url)::varchar ELSE avatar_url END
WHERE id = sqlc.arg(id)
RETURNING id, email, is_active, is_superuser, is_verified,
          display_name, avatar_url;

-- name: DeactivateAccount :exec
-- `delete_current_user_account`'s first statement. Deactivating is the whole
-- lockout: get_auth_context turns an inactive account away on every credential
-- path, so the account is shut out the moment this commits and the erasure runs
-- afterwards.
UPDATE "user" SET is_active = false WHERE id = $1;

-- name: ReactivateAccount :exec
-- The revert, for an erasure that could not be queued. Without it the account
-- is locked out of a deletion that never runs, which is what the Python
-- route's own `except` says.
UPDATE "user" SET is_active = true WHERE id = $1;

-- name: RevokeEveryRefreshToken :exec
-- `app/utils/refresh_tokens.py:revoke_all_user_tokens`, which signs every
-- device out. It writes both columns to the same instant, as that function
-- does, and it does **not** filter on `revoked_at IS NULL`: the Python UPDATE
-- has no such predicate, so a row already revoked has its two timestamps
-- rewritten.
UPDATE refresh_tokens SET revoked_at = $2, expires_at = $2 WHERE user_id = $1;

-- name: ForgetPushDevices :exec
-- `_forget_push_devices` with no family list, which is every push registration
-- the account has. It runs in the same transaction as the revoke above because
-- that is one `async with async_session_maker()` there, and a phone whose
-- sign-in has ended must not keep taking the account's pushes.
DELETE FROM push_devices WHERE user_id = $1;

-- name: AccountSettings :one
-- The settings that are the account's own: its standing instructions to the
-- chat, whether answers carry citations, and whether API keys and agents may
-- act for it. One row by primary key.
SELECT chat_instructions, citations_enabled, api_access_enabled
FROM "user"
WHERE id = $1;

-- name: UpdateAccountSettings :one
-- Writes the settings the request mentioned and leaves the others, in one
-- statement, and answers with the row as it then stands. All three columns
-- are NOT NULL, so NULL here can only mean "not mentioned" and COALESCE says
-- exactly that; the route never sends a NULL for a setting it was asked to
-- change.
UPDATE "user"
SET chat_instructions  = COALESCE(sqlc.narg(chat_instructions)::text, chat_instructions),
    citations_enabled  = COALESCE(sqlc.narg(citations_enabled)::boolean, citations_enabled),
    api_access_enabled = COALESCE(sqlc.narg(api_access_enabled)::boolean, api_access_enabled),
    updated_at         = now()
WHERE id = sqlc.arg(id)
RETURNING chat_instructions, citations_enabled, api_access_enabled;
