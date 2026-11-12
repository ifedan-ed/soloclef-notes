-- An account's mailboxes: the `accounts` list in the config of its one
-- EMAIL_IMAP_CONNECTOR connection. Every statement names the owner.

-- name: LockAccount :one
-- Taken first by the write that adds a mailbox, so that two adds by one
-- account at the same moment queue: the second finds the connection the
-- first made instead of making another.
SELECT id FROM "user" WHERE id = sqlc.arg(user_id) FOR UPDATE;

-- name: MailboxesOfOwner :many
-- Every mailbox record of the owner, in the order they were added. A
-- config whose `accounts` is not a list has none.
SELECT entry.record::jsonb AS record
FROM search_source_connectors c,
     jsonb_array_elements(c.config -> 'accounts') WITH ORDINALITY AS entry(record, position)
WHERE c.user_id = sqlc.arg(user_id)
  AND c.connector_type = 'EMAIL_IMAP_CONNECTOR'
  AND jsonb_typeof(c.config -> 'accounts') = 'array'
ORDER BY c.id, entry.position;

-- name: MailboxOfOwner :one
-- One mailbox record of the owner, by the mailbox's own id.
SELECT entry.record::jsonb AS record
FROM search_source_connectors c,
     jsonb_array_elements(c.config -> 'accounts') AS entry(record)
WHERE c.user_id = sqlc.arg(user_id)
  AND c.connector_type = 'EMAIL_IMAP_CONNECTOR'
  AND jsonb_typeof(c.config -> 'accounts') = 'array'
  AND entry.record ->> 'id' = sqlc.arg(account_id)::text
ORDER BY c.id LIMIT 1;

-- name: MailConnectionOfOwner :one
-- The owner's mail connection, locked for the add.
SELECT id FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id) AND connector_type = 'EMAIL_IMAP_CONNECTOR'
ORDER BY id LIMIT 1
FOR UPDATE;

-- name: InsertMailConnection :exec
INSERT INTO search_source_connectors (user_id, name, connector_type, is_indexable, config)
VALUES (sqlc.arg(user_id), sqlc.arg(name), 'EMAIL_IMAP_CONNECTOR', false,
        jsonb_build_object('accounts', jsonb_build_array(sqlc.arg(record)::jsonb)));

-- name: AppendMailbox :execrows
-- Adds one record to the end of the list, inside the statement: the list
-- is never read out, changed and written back, so a mailbox added or
-- removed at the same moment is not lost. A list that is missing or is not
-- a list becomes one.
UPDATE search_source_connectors
SET config = jsonb_set(
        CASE WHEN jsonb_typeof(config) = 'object' THEN config ELSE '{}'::jsonb END,
        '{accounts}',
        CASE WHEN jsonb_typeof(config -> 'accounts') = 'array' THEN config -> 'accounts' ELSE '[]'::jsonb END
            || jsonb_build_array(sqlc.arg(record)::jsonb),
        true),
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND connector_type = 'EMAIL_IMAP_CONNECTOR';

-- name: RemoveMailbox :execrows
-- Takes one mailbox out of the list, inside the statement, keeping the
-- others in their order. Only a row that holds the mailbox is touched, so
-- the count is the answer: 0 is "not found".
UPDATE search_source_connectors c
SET config = jsonb_set(c.config, '{accounts}',
        (SELECT COALESCE(jsonb_agg(entry.record ORDER BY entry.position), '[]'::jsonb)
         FROM jsonb_array_elements(c.config -> 'accounts') WITH ORDINALITY AS entry(record, position)
         WHERE entry.record ->> 'id' IS DISTINCT FROM sqlc.arg(account_id)::text),
        true),
    updated_at = now()
WHERE c.user_id = sqlc.arg(user_id)
  AND c.connector_type = 'EMAIL_IMAP_CONNECTOR'
  AND jsonb_typeof(c.config -> 'accounts') = 'array'
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(c.config -> 'accounts') AS held(record)
              WHERE held.record ->> 'id' = sqlc.arg(account_id)::text);
