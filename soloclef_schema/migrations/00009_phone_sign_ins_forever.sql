-- +goose Up

-- Phone sign-ins last five years, or a year unused (2026-11-12).
--
-- The owner's rule for every phone app is that it "should never be signed
-- out". A phone's sign-in used to last 365 days from the sign-in with no idle
-- limit; it now lasts five years from the sign-in, or a year without a
-- refresh, whichever comes first (soloclef_api/internal/signin, lifetimes.go).
-- New sign-ins get that from the API; this moves the phones already signed
-- in, so nobody is signed out on the old date.
--
-- Only a phone's live token moves: the one unspent, unexpired row of each
-- sign-in, which is the row every refresh and the gate read. A sign-in that
-- already ended (signed out, ended by a new password or a replayed token, or
-- past its old year) stays ended: moving a date is not signing anyone in.
-- Spent rows keep their dates; the grace window and the sweep read those.
--
-- The cap is five years from the sign-in (signed_in_at, which every Go row
-- carries; for an older row the old cap less its year), and the token lives a
-- year from its own row, which is the sign-in's last refresh, so "a year
-- unused" counts from the last time the phone was used. Neither date moves
-- earlier than it was: the old expiry was a year from the sign-in, and the
-- last refresh is never before the sign-in.
UPDATE refresh_tokens
SET absolute_expiry = moved.cap,
    expires_at      = least(moved.cap, refresh_tokens.created_at + interval '365 days')
FROM (
    SELECT id,
           coalesce(signed_in_at, absolute_expiry - interval '365 days', created_at)
               + interval '1825 days' AS cap
    FROM refresh_tokens
    WHERE kind = 'app' AND revoked_at IS NULL AND expires_at > now()
) AS moved
WHERE refresh_tokens.id = moved.id;

-- When single sign-on last proved who holds this sign-in: set at a sign-in
-- through the identity provider, and again when the phone app confirms the
-- person through it (POST /api/v1/auth/oidc/app/confirm), copied forward by
-- every refresh like the sign-in's other facts. NULL for a password sign-in.
--
-- It exists for one decision: an account first made through single sign-on
-- has no password to ask for, so it may set one only from a sign-in the
-- provider proved within the last ten minutes (internal/signin,
-- password_change.go). Without it, anyone holding an unlocked phone could
-- give the account a password and then sign in anywhere with it.
ALTER TABLE refresh_tokens ADD COLUMN sso_proved_at timestamptz;

-- +goose Down

ALTER TABLE refresh_tokens DROP COLUMN sso_proved_at;

-- Back to a year from the sign-in with no idle limit. A phone signed in
-- more than a year ago is then past its end and must sign in again, which is
-- what the old rule says.
UPDATE refresh_tokens
SET absolute_expiry = moved.cap,
    expires_at      = moved.cap
FROM (
    SELECT id,
           coalesce(signed_in_at, absolute_expiry - interval '1825 days', created_at)
               + interval '365 days' AS cap
    FROM refresh_tokens
    WHERE kind = 'app' AND revoked_at IS NULL AND expires_at > now()
) AS moved
WHERE refresh_tokens.id = moved.id;
