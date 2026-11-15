-- +goose Up

-- Mailboxes on a JMAP server (Stalwart), and the guard every message the
-- assistant drafts must pass before it is sent.
--
-- A JMAP mailbox is a table of its own and not one more record in the
-- EMAIL_IMAP_CONNECTOR row's `accounts` list: it holds an OAuth grant that
-- is refreshed under a row lock (Stalwart rotates the refresh token on
-- every use and deletes the whole grant if a superseded one is presented,
-- so two refreshes must never race), and drafts reference it so that
-- disconnecting the mailbox removes them with it.

-- One mailbox a person connected on a JMAP server, by Stalwart's own
-- sign-in (OAuth with PKCE) or with an app password. `credentials` is the
-- whole secret part, sealed with AES-256-GCM under a key derived from the
-- deployment's secret and bound to the owner (soloclef_assistant/ownmodels,
-- NewPurposeSeal): the app password, or the refresh and access tokens. No
-- other column is a secret, and no route ever returns this one.
CREATE TABLE jmap_mail_accounts (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    label               varchar(200) NOT NULL,
    -- The https origin the person named; every address this process uses
    -- for the mailbox (session, API, download, token, revocation) is on it.
    server_url          varchar(512) NOT NULL,
    username            varchar(320) NOT NULL,
    auth                text NOT NULL,
    -- OAuth only: the client id the grant was issued to and the server's
    -- token and revocation endpoints, read from its metadata at connect
    -- time, so a refresh in any tier needs nothing but this row.
    oauth_client_id     text,
    token_endpoint      text,
    revocation_endpoint text,
    credentials         text NOT NULL,
    -- The session offered JMAP submission when the mailbox was connected
    -- or last tested: whether a draft may be made for it at all.
    can_send            boolean NOT NULL DEFAULT false,
    -- The server refused the grant or the password; nothing reads the
    -- mailbox until its owner connects it again.
    needs_reconnect     boolean NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_jmap_mail_accounts_owner_mailbox UNIQUE (user_id, server_url, username),
    CONSTRAINT ck_jmap_mail_accounts_auth CHECK (auth IN ('oauth', 'app_password')),
    CONSTRAINT ck_jmap_mail_accounts_oauth_parts
        CHECK (auth <> 'oauth' OR (oauth_client_id IS NOT NULL AND token_endpoint IS NOT NULL))
);

-- A message the assistant drafted for its owner, held here and nowhere
-- else until the owner sends or discards it. The model can make one; only
-- the owner, pressing Send on the card that shows it, can send it, and
-- what is sent is built from `message` as it stands at that moment, whose
-- hash the confirmation carries. A draft is never edited: a revision is a
-- new row and the old one is 'superseded', so each card in a conversation
-- shows the one message it was made for.
CREATE TABLE mail_drafts (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    -- Which kind of mailbox sends it; each kind has its own column below,
    -- so disconnecting a mailbox removes its drafts.
    provider         text NOT NULL,
    jmap_account_id  uuid REFERENCES jmap_mail_accounts (id) ON DELETE CASCADE,
    -- Recipients, subject, body, identity and reply headers: exactly what
    -- the card shows and what is sent (soloclef_assistant/maildrafts).
    message          jsonb NOT NULL,
    -- SHA-256 of the message's canonical form, written with it.
    content_hash     text NOT NULL,
    status           text NOT NULL DEFAULT 'pending',
    superseded_by    uuid REFERENCES mail_drafts (id) ON DELETE SET NULL,
    -- Why the last send failed, in words for the owner.
    failure          text,
    provider_message_id text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL,
    -- When a send was begun (counted by the hourly limit, failed or not)
    -- and when it was accepted by the server.
    sending_at       timestamptz,
    sent_at          timestamptz,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_mail_drafts_provider CHECK (provider = 'jmap' AND jmap_account_id IS NOT NULL),
    CONSTRAINT ck_mail_drafts_status
        CHECK (status IN ('pending', 'sending', 'sent', 'failed', 'discarded', 'superseded'))
);
-- The hourly limit counts an owner's sends begun in the last hour.
CREATE INDEX ix_mail_drafts_owner_sending ON mail_drafts (user_id, sending_at) WHERE sending_at IS NOT NULL;
CREATE INDEX ix_mail_drafts_jmap_account ON mail_drafts (jmap_account_id);

-- One go-ahead to send one exact message: issued to the owner's browser
-- when the card shows the draft, bound to the draft, the owner and the
-- hash of what was shown, short-lived and spent by the first send that
-- presents it. Only the token's hash is stored: the token itself is in
-- the owner's browser and nowhere else, never in a tool result the model
-- reads.
CREATE TABLE mail_send_confirmations (
    token_hash   bytea PRIMARY KEY,
    draft_id     uuid NOT NULL REFERENCES mail_drafts (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    content_hash text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    used_at      timestamptz
);
CREATE INDEX ix_mail_send_confirmations_draft ON mail_send_confirmations (draft_id);
CREATE INDEX ix_mail_send_confirmations_owner_expiry ON mail_send_confirmations (user_id, expires_at);

-- +goose Down

DROP TABLE mail_send_confirmations;
DROP TABLE mail_drafts;
DROP TABLE jmap_mail_accounts;
