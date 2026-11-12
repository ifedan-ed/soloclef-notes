-- The statements of single sign-on's account matching (link.go). They run in
-- one transaction per sign-in.

-- name: LinkedAccount :one
-- The account an identity signed in to before, found by the provider and its
-- subject id (uq_oauth_account_provider_subject), whatever address the
-- identity carries today.
SELECT u.id, u.is_active
FROM oauth_account AS l
JOIN "user" AS u ON u.id = l.user_id
WHERE l.oauth_name = sqlc.arg(provider) AND l.account_id = sqlc.arg(subject);

-- name: AccountWithAddress :one
-- The account whose address is the identity's, in any case
-- (uq_user_email_lower), and whether that account's own address was ever
-- verified: nothing links to one that was not.
SELECT id, is_verified, is_active
FROM "user"
WHERE lower(email) = lower(sqlc.arg(email));

-- name: CreateAccount :one
-- A first sign-in's account, where the deployment allows one. Verified: the
-- provider vouched for the address. No password: there is none, and no
-- placeholder hash is stored for one nobody has. An address that got an
-- account a moment ago (a second tab) inserts nothing and answers no row.
INSERT INTO "user" (email, is_verified, display_name)
VALUES (sqlc.arg(email), true, sqlc.narg(display_name))
ON CONFLICT ((lower(email))) DO NOTHING
RETURNING id;

-- name: LinkIdentity :exec
-- The identity's link to its account, made at its first sign-in. Two first
-- sign-ins of one identity at once make one link.
INSERT INTO oauth_account (user_id, oauth_name, account_id, account_email)
VALUES (sqlc.arg(user_id), sqlc.arg(provider), sqlc.arg(subject), sqlc.arg(email))
ON CONFLICT (oauth_name, account_id) DO NOTHING;

-- name: RefreshLinkAddress :exec
-- The address the provider has for a linked identity today. It names the
-- account as well as the identity, so it can only touch that account's row.
UPDATE oauth_account SET account_email = sqlc.arg(email)
WHERE oauth_name = sqlc.arg(provider) AND account_id = sqlc.arg(subject)
  AND user_id = sqlc.arg(user_id);

-- name: RecordSignIn :exec
-- The moment of the sign-in, and the person's name where the account has
-- none: a name they set themselves is never written over.
UPDATE "user"
SET last_login = sqlc.arg(signed_in_at),
    display_name = CASE
        WHEN coalesce(display_name, '') = '' AND sqlc.arg(name)::text <> '' THEN sqlc.arg(name)::text
        ELSE display_name
    END
WHERE id = sqlc.arg(user_id);

-- name: AccountIsActive :one
-- Whether the account a phone's one-time code stands for may still sign in:
-- it may have been deactivated in the minute between the callback and the
-- exchange.
SELECT is_active FROM "user" WHERE id = sqlc.arg(user_id);
