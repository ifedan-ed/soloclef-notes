-- An account's mailboxes as the chat's mail tools read them: the records
-- in the `accounts` list of its one EMAIL_IMAP_CONNECTOR connection's
-- config. Every statement names the owner.

-- name: EnabledMailboxesOfOwner :many
-- Every mailbox record of the owner, in the order they were added, for a
-- turn of the chat. A connection the account switched off keeps its
-- mailboxes and is left alone by the chat, as every other kind of
-- connection is; a config whose `accounts` is not a list has none.
SELECT entry.record::jsonb AS record
FROM search_source_connectors c,
     jsonb_array_elements(c.config -> 'accounts') WITH ORDINALITY AS entry(record, position)
WHERE c.user_id = sqlc.arg(user_id)
  AND c.connector_type = 'EMAIL_IMAP_CONNECTOR'
  AND c.enabled
  AND jsonb_typeof(c.config -> 'accounts') = 'array'
ORDER BY c.id, entry.position;

-- name: MergeMailboxFields :execrows
-- Merges keys into one Microsoft mailbox's record, inside the statement:
-- a refreshed token, or the flag that says to reconnect. The list is never
-- read out and written back, so a mailbox added or removed at the same
-- moment is not lost, and the others keep their order. Only a row that
-- holds the mailbox is touched: 0 is "not the owner's, or gone".
UPDATE search_source_connectors c
SET config = jsonb_set(c.config, '{accounts}',
        (SELECT jsonb_agg(
                    CASE WHEN entry.record ->> 'id' = sqlc.arg(account_id)::text
                              AND entry.record ->> 'auth' = 'microsoft'
                         THEN entry.record || sqlc.arg(fields)::jsonb
                         ELSE entry.record END
                    ORDER BY entry.position)
         FROM jsonb_array_elements(c.config -> 'accounts') WITH ORDINALITY AS entry(record, position)),
        true),
    updated_at = now()
WHERE c.user_id = sqlc.arg(user_id)
  AND c.connector_type = 'EMAIL_IMAP_CONNECTOR'
  AND jsonb_typeof(c.config -> 'accounts') = 'array'
  AND EXISTS (SELECT 1 FROM jsonb_array_elements(c.config -> 'accounts') AS held(record)
              WHERE held.record ->> 'id' = sqlc.arg(account_id)::text
                AND held.record ->> 'auth' = 'microsoft');

-- name: MailParserOfOwner :one
-- The parser the owner's mail connection chose for its attachments; NULL
-- (no choice) is the deployment's default, as for an upload. The
-- connection switched on, oldest first, as the mailboxes are read.
SELECT parser FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id)
  AND connector_type = 'EMAIL_IMAP_CONNECTOR'
  AND enabled
ORDER BY id LIMIT 1;
