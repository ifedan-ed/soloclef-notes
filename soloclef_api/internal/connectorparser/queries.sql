-- name: ParserOfOwnedConnection :one
-- The parser a connection's files are sent to, for the account that owns
-- the connection. NULL is a connection nobody has chosen for: its files get
-- the deployment's named default (SOLOCLEF_PARSER_DEFAULT), exactly as a
-- document with no parser does.
SELECT id, parser FROM search_source_connectors WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(owner)::uuid;

-- name: SetParserOfOwnedConnection :one
-- Record the choice. One statement, scoped to the owner, so there is no
-- read-then-write for another request to slip between; no row back means
-- the connection is not this account's, or does not exist.
UPDATE search_source_connectors
SET parser = sqlc.arg(parser)::text
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(owner)::uuid
RETURNING id, parser;
