-- An account's JMAP mailboxes. Every statement names the owner: a mailbox
-- of another account is the same "no row" as one that never was.

-- name: AccountsOfOwner :many
-- The owner's mailboxes, oldest first, without their credentials: what a
-- list shows and what the chat's tools are told about.
SELECT id, label, server_url, username, auth, can_send, needs_reconnect, created_at
FROM jmap_mail_accounts
WHERE user_id = sqlc.arg(user_id)
ORDER BY created_at, id;

-- name: AccountOfOwner :one
-- One mailbox with its sealed credentials, to sign in with.
SELECT id, label, server_url, username, auth, oauth_client_id, token_endpoint, revocation_endpoint,
       credentials, can_send, needs_reconnect, created_at
FROM jmap_mail_accounts
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: LockAccountOfOwner :one
-- The same, locked until the transaction ends: a token refresh holds it,
-- so two refreshes of one grant queue and the second finds the first's
-- tokens instead of presenting a refresh token the server already
-- replaced (which makes Stalwart delete the whole grant).
SELECT id, label, server_url, username, auth, oauth_client_id, token_endpoint, revocation_endpoint,
       credentials, can_send, needs_reconnect, created_at
FROM jmap_mail_accounts
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
FOR UPDATE;

-- name: LockOwner :one
-- Taken first by a save, so two connects of one mailbox at once queue.
-- NO KEY UPDATE leaves the owner's other writes (rows that reference the
-- account) free to go on meanwhile.
SELECT id FROM "user" WHERE id = sqlc.arg(user_id) FOR NO KEY UPDATE;

-- name: MailboxOfOwnerByAddress :one
-- The owner's mailbox of this user on this server, if one is already
-- connected: a connect of it again replaces its credentials in place, and
-- the old grant is revoked by the caller.
SELECT id, label, server_url, username, auth, oauth_client_id, token_endpoint, revocation_endpoint,
       credentials, can_send, needs_reconnect, created_at
FROM jmap_mail_accounts
WHERE user_id = sqlc.arg(user_id) AND server_url = sqlc.arg(server_url) AND username = sqlc.arg(username)
FOR UPDATE;

-- name: InsertAccount :one
INSERT INTO jmap_mail_accounts (id, user_id, label, server_url, username, auth, oauth_client_id,
                                token_endpoint, revocation_endpoint, credentials, can_send)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(label), sqlc.arg(server_url), sqlc.arg(username),
        sqlc.arg(auth), sqlc.narg(oauth_client_id), sqlc.narg(token_endpoint), sqlc.narg(revocation_endpoint),
        sqlc.arg(credentials), sqlc.arg(can_send))
RETURNING created_at;

-- name: ReplaceAccount :exec
-- A mailbox connected again: new credentials and how they were given,
-- the label only when one was typed, and nothing left to reconnect.
UPDATE jmap_mail_accounts
SET label = COALESCE(sqlc.narg(label), label),
    auth = sqlc.arg(auth),
    oauth_client_id = sqlc.narg(oauth_client_id),
    token_endpoint = sqlc.narg(token_endpoint),
    revocation_endpoint = sqlc.narg(revocation_endpoint),
    credentials = sqlc.arg(credentials),
    can_send = sqlc.arg(can_send),
    needs_reconnect = false,
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: WriteCredentials :execrows
-- A refreshed pair of tokens, written under the lock LockAccountOfOwner took.
UPDATE jmap_mail_accounts
SET credentials = sqlc.arg(credentials), needs_reconnect = false, updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: MarkReconnect :execrows
-- The server refused the grant or the password.
UPDATE jmap_mail_accounts
SET needs_reconnect = true, updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: SetCanSend :execrows
-- What a test of the mailbox found about sending.
UPDATE jmap_mail_accounts
SET can_send = sqlc.arg(can_send), updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: DeleteAccountOfOwner :one
-- Removes a mailbox (its drafts go with it) and hands back what the caller
-- needs to revoke its grant at the server.
DELETE FROM jmap_mail_accounts
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING id, label, server_url, username, auth, oauth_client_id, token_endpoint, revocation_endpoint,
          credentials, can_send, needs_reconnect, created_at;
