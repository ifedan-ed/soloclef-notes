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
-- This phone, for this account, from now on, with its token's kind (and,
-- for an APNs token, the environment it was minted in) and the app version
-- it runs (NULL from a build that does not say). A token is one phone's,
-- and a phone shows one account at a time: when someone signs in on it as
-- another account, its token moves to that account (the one with a live
-- sign-in on it now) and the last stops being pushed to it.
--
-- A sign-in is one app on one phone, so it holds one token: one it
-- registered before (the Expo token an iPhone had before it moved to APNs,
-- or a token the phone's service replaced) is deleted in the same
-- statement, so the phone is never pushed twice, once on each.
--
-- The App Store version the phone was last told about
-- (update_notified_version) belongs to the phone, not to its token or its
-- account: a replaced token hands it to the new one, and a token that moves
-- account keeps it, so a phone is never told about the same version twice
-- because its token changed (soloclef_notify/notify, AppUpdated).
WITH replaced AS (
    DELETE FROM push_devices
    WHERE user_id = sqlc.arg(user_id)
      AND session_family_id = sqlc.arg(family_id)::uuid
      AND token <> sqlc.arg(token)
    RETURNING update_notified_version
)
INSERT INTO push_devices (user_id, token, kind, apns_environment, platform, label, language, app_version,
                          update_id, runtime_version,
                          update_notified_version, session_family_id, last_seen_at)
VALUES (sqlc.arg(user_id), sqlc.arg(token), sqlc.arg(kind), sqlc.narg(apns_environment), sqlc.arg(platform),
        sqlc.narg(label), sqlc.narg(language), sqlc.narg(app_version),
        sqlc.narg(update_id)::uuid, sqlc.narg(runtime_version),
        (SELECT r.update_notified_version FROM replaced r WHERE r.update_notified_version IS NOT NULL LIMIT 1),
        sqlc.arg(family_id)::uuid, sqlc.arg(now)::timestamptz)
ON CONFLICT (token) DO UPDATE SET
    user_id = EXCLUDED.user_id,
    kind = EXCLUDED.kind,
    apns_environment = EXCLUDED.apns_environment,
    platform = EXCLUDED.platform,
    label = EXCLUDED.label,
    language = EXCLUDED.language,
    app_version = EXCLUDED.app_version,
    update_id = EXCLUDED.update_id,
    runtime_version = EXCLUDED.runtime_version,
    update_notified_version = COALESCE(push_devices.update_notified_version, EXCLUDED.update_notified_version),
    session_family_id = EXCLUDED.session_family_id,
    last_seen_at = EXCLUDED.last_seen_at
RETURNING id, token, kind, platform, label, language, app_version, update_id, runtime_version, last_seen_at;

-- name: RemoveDevice :exec
-- Forgetting a phone at sign-out. Only the account it is registered to can.
DELETE FROM push_devices WHERE user_id = sqlc.arg(user_id) AND token = sqlc.arg(token);

-- name: Preferences :one
SELECT choices, quiet_start_minute, quiet_end_minute, time_zone FROM push_preferences WHERE user_id = sqlc.arg(user_id);

-- name: SavePreferences :one
-- The kinds given, over the ones saved: a kind not named keeps its choice.
-- Quiet hours are written only when the request named them
-- (set_quiet), and then as given: three values, or three NULLs for off.
-- One statement, so a change to the switches and one to the quiet hours
-- sent together are saved together.
INSERT INTO push_preferences (user_id, choices, quiet_start_minute, quiet_end_minute, time_zone, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(choices)::jsonb,
        CASE WHEN sqlc.arg(set_quiet)::boolean THEN sqlc.narg(quiet_start)::smallint END,
        CASE WHEN sqlc.arg(set_quiet)::boolean THEN sqlc.narg(quiet_end)::smallint END,
        CASE WHEN sqlc.arg(set_quiet)::boolean THEN sqlc.narg(time_zone)::text END,
        sqlc.arg(now)::timestamptz)
ON CONFLICT (user_id) DO UPDATE SET
    choices = push_preferences.choices || EXCLUDED.choices,
    quiet_start_minute = CASE WHEN sqlc.arg(set_quiet)::boolean THEN EXCLUDED.quiet_start_minute ELSE push_preferences.quiet_start_minute END,
    quiet_end_minute = CASE WHEN sqlc.arg(set_quiet)::boolean THEN EXCLUDED.quiet_end_minute ELSE push_preferences.quiet_end_minute END,
    time_zone = CASE WHEN sqlc.arg(set_quiet)::boolean THEN EXCLUDED.time_zone ELSE push_preferences.time_zone END,
    updated_at = EXCLUDED.updated_at
RETURNING choices, quiet_start_minute, quiet_end_minute, time_zone;
