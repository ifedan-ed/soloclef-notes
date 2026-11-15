-- The send guard's rows: drafts, the go-aheads to send them, and the
-- activity log entry every send writes. Every statement names the owner.

-- name: LockOwner :one
-- Taken first by a send, so an owner's sends queue and the count of the
-- past hour each one checks (an unusual pace is confirmed twice) includes
-- the one before it. NO KEY UPDATE, not UPDATE:
-- it must not block the key-share lock a row that references the owner
-- takes (the activity entry a finishing send writes), or a send finishing
-- and a send beginning wait on each other.
SELECT id FROM "user" WHERE id = sqlc.arg(user_id) FOR NO KEY UPDATE;

-- name: InsertDraft :exec
INSERT INTO mail_drafts (id, user_id, provider, jmap_account_id, message, content_hash, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(provider), sqlc.arg(jmap_account_id),
        sqlc.arg(message)::jsonb, sqlc.arg(content_hash), sqlc.arg(expires_at));

-- name: SupersedeDraft :execrows
-- A revision replaces a draft only while it is still waiting: a sent or
-- discarded one stays what it was.
UPDATE mail_drafts
SET status = 'superseded', superseded_by = sqlc.arg(superseded_by), updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status IN ('pending', 'failed');

-- name: DraftOfOwner :one
SELECT id, provider, jmap_account_id, message::jsonb AS message, content_hash, status, superseded_by,
       failure, provider_message_id, created_at, expires_at, sending_at, sent_at
FROM mail_drafts
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: LockDraftOfOwner :one
SELECT id, provider, jmap_account_id, message::jsonb AS message, content_hash, status, superseded_by,
       failure, provider_message_id, created_at, expires_at, sending_at, sent_at
FROM mail_drafts
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
FOR UPDATE;

-- name: ForgetSpentConfirmations :exec
-- An owner's go-aheads that can no longer be used, removed as a new one is
-- issued, so the table holds only the few that are live.
DELETE FROM mail_send_confirmations
WHERE user_id = sqlc.arg(user_id) AND (expires_at < sqlc.arg(now) OR used_at IS NOT NULL);

-- name: InsertConfirmation :exec
INSERT INTO mail_send_confirmations (token_hash, draft_id, user_id, content_hash, unusual, created_at, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(draft_id), sqlc.arg(user_id), sqlc.arg(content_hash),
        sqlc.arg(unusual)::text[], sqlc.arg(created_at), sqlc.arg(expires_at));

-- name: LockConfirmation :one
-- The go-aheads are looked up by the hash of the token the browser
-- presented; whose it is and which draft it names are checked by the
-- caller, so a token of another account is told apart from none only in
-- the log.
SELECT token_hash, draft_id, user_id, content_hash, unusual, expires_at, used_at
FROM mail_send_confirmations
WHERE token_hash = sqlc.arg(token_hash)
FOR UPDATE;

-- name: SpendConfirmation :execrows
UPDATE mail_send_confirmations
SET used_at = sqlc.arg(used_at)
WHERE token_hash = sqlc.arg(token_hash) AND used_at IS NULL;

-- name: SendsSince :one
-- How many sends the owner began after the moment given: the pace point,
-- a send past UnusualSendsPerHour in an hour being confirmed twice.
SELECT count(*) FROM mail_drafts
WHERE user_id = sqlc.arg(user_id) AND sending_at > sqlc.arg(since);

-- name: BeginSending :execrows
UPDATE mail_drafts
SET status = 'sending', sending_at = sqlc.arg(sending_at), failure = NULL, updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status IN ('pending', 'failed');

-- name: FinishSent :execrows
UPDATE mail_drafts
SET status = 'sent', sent_at = sqlc.arg(sent_at), provider_message_id = sqlc.narg(provider_message_id),
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status = 'sending';

-- name: FinishFailed :execrows
UPDATE mail_drafts
SET status = 'failed', failure = sqlc.arg(failure), updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status = 'sending';

-- name: DiscardDraft :execrows
UPDATE mail_drafts
SET status = 'discarded', updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status IN ('pending', 'failed');

-- name: WriteActivity :exec
-- The entry in the owner's activity log (the Logs page) a send writes,
-- whether it was accepted or failed.
INSERT INTO logs (user_id, level, status, message, source, log_metadata, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(level)::loglevel, sqlc.arg(status)::logstatus, sqlc.arg(message),
        sqlc.arg(source)::varchar, sqlc.arg(log_metadata)::jsonb, sqlc.arg(created_at));

-- name: WaitingDraftsOfOwner :many
-- The owner's drafts that still wait for them, newest first, for the
-- Drafts waiting page: one that can be sent (waiting or failed, not run
-- out) or is being sent right now. The card fetches the rest itself, so
-- only what orders and names the list is read here. A sending draft is
-- listed whatever its expiry, because its send has begun and the page must
-- show how it ends. mail_drafts_user_id_idx keeps it to the owner's rows.
-- The mailbox's label is read beside it so a list of several mailboxes'
-- drafts says which is which before any card has loaded.
SELECT d.id, d.status, coalesce(d.message->>'subject', '')::text AS subject,
       coalesce(a.label, '')::text AS account_label, d.created_at, d.expires_at
FROM mail_drafts d
LEFT JOIN jmap_mail_accounts a ON a.id = d.jmap_account_id AND a.user_id = d.user_id
WHERE d.user_id = sqlc.arg(user_id)
  AND (d.status = 'sending' OR (d.status IN ('pending', 'failed') AND d.expires_at > sqlc.arg(now)))
ORDER BY d.created_at DESC, d.id
LIMIT sqlc.arg(most);
