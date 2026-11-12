-- A phone's push registration and the account's choices of what to push.
-- Every statement names the account, so one account never reads, changes or
-- removes another's choices or devices.

-- name: FamilyIsLive :one
-- Whether the sign-in a phone registers under is one of this account's, and
-- still usable: a phone the server signed out (from the devices list, by a
-- password change, everywhere) must not come back unbound on an access token
-- that has not run out yet.
SELECT EXISTS (
    SELECT 1 FROM refresh_tokens
    WHERE user_id = sqlc.arg(user_id)
      AND family_id = sqlc.arg(family_id)::uuid
      AND revoked_at IS NULL
      AND expires_at > sqlc.arg(now)::timestamptz
      AND (absolute_expiry IS NULL OR absolute_expiry > sqlc.arg(now)::timestamptz)
) AS live;

-- name: UpsertDevice :one
-- This phone, for this account, from now on. A token is one phone's, and a
-- phone shows one account at a time: when someone signs in on it as another
-- account, its token moves to that account (the one with a live sign-in on
-- it now) and the last stops being pushed to it.
INSERT INTO push_devices (user_id, token, platform, label, language, session_family_id, last_seen_at)
VALUES (sqlc.arg(user_id), sqlc.arg(token), sqlc.arg(platform), sqlc.narg(label), sqlc.narg(language),
        sqlc.arg(family_id)::uuid, sqlc.arg(now)::timestamptz)
ON CONFLICT (token) DO UPDATE SET
    user_id = EXCLUDED.user_id,
    platform = EXCLUDED.platform,
    label = EXCLUDED.label,
    language = EXCLUDED.language,
    session_family_id = EXCLUDED.session_family_id,
    last_seen_at = EXCLUDED.last_seen_at
RETURNING id, token, platform, label, language, last_seen_at;

-- name: RemoveDevice :exec
-- Forgetting a phone at sign-out. Only the account it is registered to can.
DELETE FROM push_devices WHERE user_id = sqlc.arg(user_id) AND token = sqlc.arg(token);

-- name: Choices :one
SELECT choices FROM push_preferences WHERE user_id = sqlc.arg(user_id);

-- name: SaveChoices :one
-- The kinds given, over the ones saved: a kind not named keeps its choice.
INSERT INTO push_preferences (user_id, choices, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(choices)::jsonb, sqlc.arg(now)::timestamptz)
ON CONFLICT (user_id) DO UPDATE SET
    choices = push_preferences.choices || EXCLUDED.choices,
    updated_at = EXCLUDED.updated_at
RETURNING choices;
