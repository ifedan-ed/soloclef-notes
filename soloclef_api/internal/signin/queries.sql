-- The statements of sign-in. Every one that touches a refresh token names
-- the token by its hash (the plaintext is never stored), and every one that
-- acts on a whole sign-in names the account beside the family, so one
-- account can never end another's sign-in by guessing a family id.

-- name: AccountForSignIn :one
-- The account an address signs in as. `lower(email)` is what the unique
-- index is on, so two spellings of one address are one account.
SELECT id, hashed_password, is_active
FROM "user"
WHERE lower(email) = lower(sqlc.arg(email));

-- name: AccountIsActive :one
SELECT is_active FROM "user" WHERE id = sqlc.arg(user_id);

-- name: RecordSignIn :exec
-- The moment of the sign-in, and the stronger hash when the stored one was
-- made with weaker settings than this tier's (passwords.go). A NULL
-- `rehashed` keeps the stored hash.
UPDATE "user"
SET last_login = sqlc.arg(signed_in_at),
    hashed_password = coalesce(sqlc.narg(rehashed), hashed_password)
WHERE id = sqlc.arg(user_id);

-- name: InsertRefreshToken :exec
-- created_at is the tier's clock, the one every expiry beside it is
-- computed from: the devices list dates a device's last use by it.
-- sso_proved_at is when single sign-on last proved the sign-in (NULL for a
-- password sign-in); a refresh copies it forward with the rest.
INSERT INTO refresh_tokens (
    user_id, token_hash, expires_at, family_id, absolute_expiry, kind,
    device_label, signed_in_at, sso_proved_at, created_at
) VALUES (
    sqlc.arg(user_id), sqlc.arg(token_hash), sqlc.arg(expires_at),
    sqlc.arg(family_id), sqlc.arg(absolute_expiry), sqlc.arg(kind),
    sqlc.narg(device_label), sqlc.arg(signed_in_at), sqlc.narg(sso_proved_at),
    sqlc.arg(created_at)
);

-- name: LockRefreshToken :one
-- The token a refresh presents, locked for the rotation, so that two
-- refreshes racing with one token cannot both mint a successor.
SELECT id, user_id, expires_at, revoked_at, absolute_expiry, family_id,
       kind, device_label, signed_in_at, sso_proved_at, created_at
FROM refresh_tokens
WHERE token_hash = sqlc.arg(token_hash)
FOR UPDATE;

-- name: RetireRefreshToken :exec
-- The presented token, spent by a rotation. Its expiry stays: the grace
-- window reads it (rotation.go).
UPDATE refresh_tokens SET revoked_at = sqlc.arg(revoked_at)
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: EndFamily :execrows
-- Every token of one sign-in, ended now. Used when a spent token is
-- presented again (it was stolen, or copied) and when a device is signed
-- out. The tokens a rotation spent are capped too, not only the live one:
-- a spent token inside its grace window (rotation.go) is otherwise still
-- owed an access token, so a device signed out within 45 seconds of a
-- refresh could get one more hour. Only tokens that could still do
-- something are touched, so the count is 0 for a sign-in already over.
UPDATE refresh_tokens
SET revoked_at = coalesce(revoked_at, sqlc.arg(ended_at)), expires_at = sqlc.arg(ended_at)
WHERE family_id = sqlc.arg(family_id) AND user_id = sqlc.arg(user_id)
  AND (revoked_at IS NULL OR expires_at > sqlc.arg(ended_at));

-- name: EndTokenByHash :one
-- Signing out with a token: the token is ended, and the answer names its
-- sign-in so that the rest of the family and its phone's pushes go too.
UPDATE refresh_tokens SET revoked_at = sqlc.arg(ended_at), expires_at = sqlc.arg(ended_at)
WHERE token_hash = sqlc.arg(token_hash)
RETURNING user_id, family_id;

-- name: ForgetPushDevices :exec
-- A phone whose sign-in ended stops getting the account's pushes.
DELETE FROM push_devices
WHERE user_id = sqlc.arg(user_id) AND session_family_id = sqlc.arg(family_id);

-- name: SetAccountPassword :one
-- The operator's way to make an account or give one a new password
-- (accounts.go). An address that has an account keeps it, whatever its
-- case; `created` says which happened.
INSERT INTO "user" (email, hashed_password, is_verified)
VALUES (sqlc.arg(email), sqlc.arg(hashed_password), true)
ON CONFLICT ((lower(email))) DO UPDATE
SET hashed_password = EXCLUDED.hashed_password, updated_at = now()
RETURNING id, (xmax = 0)::boolean AS created;

-- name: EndEverySignIn :exec
-- A new password, or signing out everywhere, ends every sign-in the account
-- had, on every device; spent tokens inside their grace window too, as
-- EndFamily says why.
UPDATE refresh_tokens
SET revoked_at = coalesce(revoked_at, sqlc.arg(ended_at)), expires_at = sqlc.arg(ended_at)
WHERE user_id = sqlc.arg(user_id) AND (revoked_at IS NULL OR expires_at > sqlc.arg(ended_at));

-- name: ForgetEveryPushDevice :exec
DELETE FROM push_devices WHERE user_id = sqlc.arg(user_id);

-- name: RegisterAccount :one
-- The sign-up form's account (register.go). Unverified: nothing has shown
-- that the person holds the address. An address that already has an
-- account, in any case, inserts nothing and answers no row.
INSERT INTO "user" (email, hashed_password)
VALUES (sqlc.arg(email), sqlc.arg(hashed_password))
ON CONFLICT ((lower(email))) DO NOTHING
RETURNING id, email, is_active, is_superuser, is_verified;

-- name: AccountForReset :one
-- The account a reset link is mailed to (password_reset.go). The stored
-- address is the one mailed, not the spelling the form was given.
SELECT id, email, hashed_password, is_active
FROM "user"
WHERE lower(email) = lower(sqlc.arg(email));

-- name: AccountPasswordState :one
-- What a reset token is checked against: the password it was minted beside
-- (a token stops working the moment the password changes) and whether the
-- account may still sign in.
SELECT email, hashed_password, is_active FROM "user" WHERE id = sqlc.arg(user_id);

-- name: LockAccountPasswordState :one
-- The same, locked for the write, so two requests spending one token cannot
-- both find the old password in place.
SELECT email, hashed_password, is_active FROM "user" WHERE id = sqlc.arg(user_id)
FOR UPDATE;

-- name: ResetAccountPassword :exec
-- The new password of a reset. The address is verified with it: the token
-- arrived by mail at that address, which is what verifying one means.
UPDATE "user"
SET hashed_password = sqlc.arg(hashed_password), is_verified = true, updated_at = now()
WHERE id = sqlc.arg(user_id);

-- name: SetAdministrator :one
-- The operator's way to say who administers the deployment (accounts.go):
-- the account's own flag, which is the only thing any admin check reads.
UPDATE "user" SET is_superuser = sqlc.arg(administrator), updated_at = now()
WHERE lower(email) = lower(sqlc.arg(email))
RETURNING id;

-- name: SignedInDevices :many
-- Every device the account is signed in on: one row per sign-in (family)
-- that still has a usable token, newest use first. The sign-in is dated by
-- the stamp every token of the family carries (rows rotated away are
-- swept), or by the family's first row where an older row has none; every
-- refresh writes a row, so the family's newest row is its last use. A
-- token's own expiry is capped by its family's, so the earlier of the two is
-- when the device will have to sign in again.
WITH live AS (
    SELECT DISTINCT ON (family_id) family_id, kind, device_label, expires_at, absolute_expiry
    FROM refresh_tokens
    WHERE user_id = sqlc.arg(user_id)::uuid
      AND revoked_at IS NULL
      AND expires_at > sqlc.arg(now)::timestamptz
      AND (absolute_expiry IS NULL OR absolute_expiry > sqlc.arg(now)::timestamptz)
    ORDER BY family_id, created_at DESC, id DESC
), span AS (
    SELECT family_id,
           min(coalesce(signed_in_at, created_at))::timestamptz AS signed_in_at,
           max(created_at)::timestamptz AS last_used_at
    FROM refresh_tokens
    WHERE user_id = sqlc.arg(user_id)::uuid
    GROUP BY family_id
)
SELECT live.family_id::text AS family_id, live.kind, live.device_label,
       span.signed_in_at, span.last_used_at,
       least(live.expires_at, coalesce(live.absolute_expiry, live.expires_at))::timestamptz AS expires_at
FROM live JOIN span ON span.family_id = live.family_id
ORDER BY span.last_used_at DESC, live.family_id;

-- name: SessionSSOProof :one
-- When single sign-on last proved the caller's own sign-in, read from its
-- live token (the one row ix_refresh_tokens_live_family holds for it). No
-- row: the sign-in is over. A NULL proof: it was a password sign-in, never
-- confirmed through the provider.
SELECT sso_proved_at
FROM refresh_tokens
WHERE user_id = sqlc.arg(user_id) AND family_id = sqlc.arg(family_id)::uuid
  AND revoked_at IS NULL AND expires_at > sqlc.arg(now)
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ProveSessionBySSO :execrows
-- The phone app confirmed the person through single sign-on without a new
-- sign-in (internal/oidcsignin, the app's confirm): its own live sign-in is
-- stamped. It names the account beside the family, so a code proving one
-- account can stamp no other's sign-in; 0 rows means the sign-in is over.
UPDATE refresh_tokens SET sso_proved_at = sqlc.arg(proved_at)
WHERE user_id = sqlc.arg(user_id) AND family_id = sqlc.arg(family_id)::uuid
  AND revoked_at IS NULL AND expires_at > sqlc.arg(proved_at);

-- name: ChangeAccountPassword :exec
-- A signed-in person's new password (password_change.go). Unlike a reset it
-- verifies nothing: no mail went to the address.
UPDATE "user"
SET hashed_password = sqlc.arg(hashed_password), updated_at = sqlc.arg(changed_at)
WHERE id = sqlc.arg(user_id);

-- name: EndOtherSignIns :one
-- A new password from a signed-in person ends every sign-in of the account
-- but the one that asked, and says how many it ended. Tokens a rotation spent
-- are capped as EndEverySignIn says why; the count is of sign-ins, not rows.
WITH ended AS (
    UPDATE refresh_tokens
    SET revoked_at = coalesce(revoked_at, sqlc.arg(ended_at)), expires_at = sqlc.arg(ended_at)
    WHERE user_id = sqlc.arg(user_id) AND family_id <> sqlc.arg(kept_family_id)::uuid
      AND (revoked_at IS NULL OR expires_at > sqlc.arg(ended_at))
    RETURNING family_id
)
SELECT count(DISTINCT family_id)::bigint AS sign_ins FROM ended;

-- name: ForgetOtherPushDevices :exec
-- The phones registered under the sign-ins EndOtherSignIns ended stop getting
-- pushes; so does one registered under no sign-in at all, which nothing ties
-- to the device that asked. The asking device keeps its own.
DELETE FROM push_devices
WHERE user_id = sqlc.arg(user_id)
  AND session_family_id IS DISTINCT FROM sqlc.arg(kept_family_id)::uuid;

-- name: LogAccountEvent :exec
-- One entry in the account's own activity log (the logs table the web's and
-- the app's Activity log read), written in the transaction of the change it
-- records, so the log never says a password changed that did not.
INSERT INTO logs (user_id, level, status, message, source, log_metadata, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(level)::loglevel, sqlc.arg(status)::logstatus,
        sqlc.arg(message), sqlc.arg(source), sqlc.narg(log_metadata)::jsonb,
        sqlc.arg(created_at));
