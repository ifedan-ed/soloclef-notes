-- name: LinkedAccount :one
-- The account an identity was linked to when it signed in on the web, found by
-- the provider's subject id (app.auth_oidc.account_link.identity_links). The
-- Python tier asks for one value and takes the first row, so this asks the same
-- question with no ordering of its own.
SELECT user_id FROM oauth_account
WHERE oauth_name = $1 AND account_id = $2;

-- name: AccountWithEmail :one
-- The account whose address is this one, with the two flags the rule needs: the
-- Python tier reads the id with this filter and then loads the row by its
-- primary key, which is this row.
SELECT id, is_verified, is_active FROM "user"
WHERE lower(email) = $1;

-- name: AccountIsActive :one
-- Whether a linked account still exists and is still usable. A row that is gone
-- and a row that is deactivated are the same refusal there.
SELECT is_active FROM "user" WHERE id = $1;
