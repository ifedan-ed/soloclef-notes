-- A Microsoft mailbox in an account's mail connection: a record in the
-- `accounts` list of its one EMAIL_IMAP_CONNECTOR row's config, beside the
-- password mailboxes internal/mailaccounts keeps there. Every statement
-- names the owner. The callback, which has no session, names the account
-- its sealed cookie holds.

-- name: LockAccount :one
-- Taken first by the write that saves a mailbox, so that two connects of
-- one account finishing at the same moment queue: the second finds the
-- connection the first made instead of making another.
SELECT id FROM "user" WHERE id = sqlc.arg(user_id) FOR UPDATE;

-- name: MicrosoftMailboxOfOwner :one
-- One Microsoft mailbox record of the owner, by the mailbox's own id.
-- Another account's, a password mailbox and an id never used are all no
-- row.
SELECT entry.record::jsonb AS record
FROM search_source_connectors c,
     jsonb_array_elements(c.config -> 'accounts') AS entry(record)
WHERE c.user_id = sqlc.arg(user_id)
  AND c.connector_type = 'EMAIL_IMAP_CONNECTOR'
  AND jsonb_typeof(c.config -> 'accounts') = 'array'
  AND entry.record ->> 'id' = sqlc.arg(account_id)::text
  AND entry.record ->> 'auth' = 'microsoft'
ORDER BY c.id LIMIT 1;

-- name: MailConnectionOfOwner :one
-- The owner's mail connection and its list, locked until the save commits:
-- a mailbox added, removed or refreshed meanwhile waits for it and then
-- changes the list this save wrote.
SELECT id,
       (CASE WHEN jsonb_typeof(config -> 'accounts') = 'array' THEN config -> 'accounts' ELSE '[]'::jsonb END)::jsonb AS accounts
FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id) AND connector_type = 'EMAIL_IMAP_CONNECTOR'
ORDER BY id LIMIT 1
FOR UPDATE;

-- name: InsertMailConnection :exec
-- The account's first mailbox makes its mail connection.
INSERT INTO search_source_connectors (user_id, name, connector_type, is_indexable, config)
VALUES (sqlc.arg(user_id), sqlc.arg(name), 'EMAIL_IMAP_CONNECTOR', false,
        jsonb_build_object('accounts', sqlc.arg(accounts)::jsonb));

-- name: ReplaceMailboxes :execrows
-- Writes the list a save built from the one it read under the row's lock.
UPDATE search_source_connectors
SET config = jsonb_set(
        CASE WHEN jsonb_typeof(config) = 'object' THEN config ELSE '{}'::jsonb END,
        '{accounts}', sqlc.arg(accounts)::jsonb, true),
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'EMAIL_IMAP_CONNECTOR';
