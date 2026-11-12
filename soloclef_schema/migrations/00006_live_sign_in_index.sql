-- +goose Up

-- The gate's lookup of a sign-in's unspent token.
--
-- Every request a session makes asks whether the sign-in its access token
-- names (refresh_tokens.family_id) still has an unspent, unexpired token, so
-- that signing out, signing a device out, signing out everywhere and a new
-- password end the access token at once (soloclef_api/internal/principal,
-- PostgresDirectory.SessionIsLive). A family keeps every token it ever
-- spent: a phone that refreshes hourly for a year has thousands of rows and
-- one unspent. ix_refresh_tokens_family_id would walk all of them on every
-- request; this index holds only the unspent ones, one or two per sign-in,
-- so the check is a single probe however old the sign-in is.
--
-- Not CONCURRENTLY: refresh_tokens is small (the Go stack started empty and
-- a row is one sign-in or refresh), so the build takes milliseconds, and a
-- plain build keeps the migration in its transaction.
CREATE INDEX ix_refresh_tokens_live_family ON refresh_tokens (family_id) WHERE revoked_at IS NULL;

-- +goose Down

DROP INDEX ix_refresh_tokens_live_family;
