-- name: LinkedAccount :one
-- The account an identity was linked to when it signed in on the web, found by
-- the provider's subject id (app.auth_oidc.account_link.identity_links). The
-- Python tier asks for one value and takes the first row, so this asks the same
-- question with no ordering of its own.
SELECT user_id FROM oauth_account
WHERE oauth_name = $1 AND account_id = $2;

-- name: AccountWithEmail :one
-- The account whose address is this one, with the two flags the rule needs
-- and the account's API-access switch, which the gate checks for every agent
-- (internal/principal). The switch rides on this row so the gate asks nothing
-- of its own. uq_user_email_lower answers the filter.
SELECT id, is_verified, is_active, api_access_enabled FROM "user"
WHERE lower(email) = $1;

-- name: AccountStanding :one
-- Whether a linked account still exists and is still usable (a row that is
-- gone and a row that is deactivated are the same refusal), and its
-- API-access switch, for the same reason as above.
SELECT is_active, api_access_enabled FROM "user" WHERE id = $1;
