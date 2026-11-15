-- +goose Up

-- Four settings changes of 2026-11-13, one section each. Each section is
-- self-contained and its Down undoes only it, in reverse order below.

-- == 1. Agent permissions: tools run without asking; "ask first" is stored ==

-- The owner's rule (2026-11-13): "let default be to always allow all, then
-- settings you can change it". The chat used to ask before some tools and
-- remember "always allow" answers here; now every tool runs without asking
-- and a row is a tool the person wants to be asked about first
-- (soloclef_api/internal/agentrules). Every row in the table is an "always
-- allow" (or a rule the Python app wrote and the Go chat never read), which
-- under the new rule says nothing, so all of them go.
--
-- The scope, pattern and action columns go with them: a mark is one tool,
-- in every chat, and its existence is the whole answer. connector_id names
-- the connected app (MCP server) a tool belongs to, so one table holds every
-- mark and a removed app takes its marks with it; the chat's own tools have
-- none. The app's tools used to be trusted in the connection's
-- config.trusted_tools, which goes too.
DELETE FROM agent_permission_rules;
DROP INDEX ix_agent_permission_rules_thread_id;
ALTER TABLE agent_permission_rules
    DROP CONSTRAINT uq_agent_permission_rules_scope,
    DROP CONSTRAINT ck_agent_permission_rules_action,
    DROP COLUMN thread_id,
    DROP COLUMN pattern,
    DROP COLUMN action;
ALTER TABLE agent_permission_rules RENAME TO agent_ask_first_tools;
ALTER TABLE agent_ask_first_tools RENAME CONSTRAINT agent_permission_rules_pkey TO agent_ask_first_tools_pkey;
ALTER TABLE agent_ask_first_tools RENAME CONSTRAINT agent_permission_rules_user_id_fkey TO agent_ask_first_tools_user_id_fkey;
ALTER SEQUENCE agent_permission_rules_id_seq RENAME TO agent_ask_first_tools_id_seq;
ALTER TABLE agent_ask_first_tools RENAME COLUMN permission TO tool;
ALTER TABLE agent_ask_first_tools
    ADD COLUMN connector_id bigint REFERENCES search_source_connectors (id) ON DELETE CASCADE,
    -- NULLS NOT DISTINCT, or a chat tool's mark (no connector) could be
    -- stored twice. Leading with user_id, it is also the index for "this
    -- account's marks" and for the user foreign key.
    ADD CONSTRAINT uq_agent_ask_first_tools_tool UNIQUE NULLS NOT DISTINCT (user_id, connector_id, tool);
-- Deleting a connection finds its marks by this; most marks have none.
CREATE INDEX ix_agent_ask_first_tools_connector_id ON agent_ask_first_tools (connector_id)
    WHERE connector_id IS NOT NULL;
UPDATE search_source_connectors SET config = config - 'trusted_tools' WHERE config ? 'trusted_tools';

-- == 2. Prompts: no community prompts and no sharing ==

-- The owner's decision (2026-11-13): Soloclef is one person's notes, so
-- there is nobody to share a prompt with. The community shelf, the copy
-- from it and the "share" switch in My prompts are gone from the API and
-- the web app (soloclef_api/internal/prompts), and is_public was the only
-- thing they kept. Its partial index served the shelf's list and nothing
-- else. A prompt stays its owner's, unchanged in every other column.
DROP INDEX IF EXISTS ix_prompts_public;
ALTER TABLE prompts DROP COLUMN IF EXISTS is_public;

-- == 3. Phone sign-ins: 180 days unused, no absolute cap ==

-- The owner's rule (2026-11-13): the app "should sign out after 6 months if
-- unused ... but if always used no need for ever signing out". A phone's
-- sign-in used to end five years after the sign-in or a year unused
-- (00009); it now ends only after 180 days without a refresh, and every
-- refresh starts the 180 days again (soloclef_api/internal/signin,
-- lifetimes.go). "No cap" is a NULL absolute_expiry, which the gate, the
-- devices list and the pushes already read as none. New sign-ins get that
-- from the API; this moves the phones already signed in.
--
-- Only each sign-in's live token moves (unspent, unexpired, under its cap),
-- the one row every refresh and the gate read; as in 00009, a sign-in that
-- already ended stays ended and spent rows keep the dates the grace window
-- reads. The live row's created_at is the sign-in's last refresh (each
-- refresh writes a new row), so the new end is that plus 180 days. A phone
-- already unused for longer gets an end that has passed, which ends it now,
-- exactly as if the new rule had always applied: leaving it live would keep
-- signed in the very phone in a drawer the rule exists to sign out.
UPDATE refresh_tokens
SET absolute_expiry = NULL,
    expires_at      = created_at + interval '180 days'
WHERE kind = 'app' AND revoked_at IS NULL AND expires_at > now()
  AND (absolute_expiry IS NULL OR absolute_expiry > now());

-- == 4. Announcements: removed ==

-- The owner's decision (2026-11-13): Soloclef is one person's notes, so an
-- administrator has nobody to announce anything to. The Settings page that
-- sent them, the inbox tab that listed them, their push switch and the
-- worker's job that pushed them are gone from every tier. The copies in the
-- inbox go first: no client draws a site_announcement row any more, so one
-- left behind would only count as unread under a bell that cannot show it.
-- A saved push choice for the kind is dropped with it; the API already
-- ignores a key that is no longer a kind, so this only keeps the row honest.
DELETE FROM notifications WHERE type = 'site_announcement';
UPDATE push_preferences SET choices = choices - 'announcement' WHERE choices ? 'announcement';
DROP TABLE site_announcements;

-- +goose Down

-- == 4. Announcements ==

-- The table comes back as 00001 made it, empty. What was sent, and every
-- inbox copy and push choice of it, cannot come back: the Up deleted them.
-- A message from the operator to every account.
CREATE TABLE site_announcements (
    id                 bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    title              varchar(200) NOT NULL,
    body               text NOT NULL,
    link               varchar(2048),
    created_by_id      uuid REFERENCES "user" (id) ON DELETE SET NULL,
    expires_at         timestamptz,
    withdrawn_at       timestamptz,
    recipient_count    integer NOT NULL DEFAULT 0,
    cleared_read_count integer NOT NULL DEFAULT 0,
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_site_announcements_created_by_id ON site_announcements (created_by_id);

-- == 3. Phone sign-ins ==

-- Back to 00009's rule for the phones still signed in: five years from the
-- sign-in (signed_in_at, which every Go row carries; a row without one
-- counts from its own row, as 00009 did), or a year from the last refresh,
-- whichever is earlier. A phone the Up ended for 180 days unused stays
-- ended: moving a date back is not signing anyone in.
UPDATE refresh_tokens
SET absolute_expiry = moved.cap,
    expires_at      = least(moved.cap, refresh_tokens.created_at + interval '365 days')
FROM (
    SELECT id, coalesce(signed_in_at, created_at) + interval '1825 days' AS cap
    FROM refresh_tokens
    WHERE kind = 'app' AND revoked_at IS NULL AND expires_at > now()
) AS moved
WHERE refresh_tokens.id = moved.id;

-- == 2. Prompts ==

-- The column and its index come back as 00001 made them. Which prompts were
-- shared cannot come back: the Up dropped that, so every prompt returns
-- private (the column's default), which is also what nobody sharing means.
ALTER TABLE prompts ADD COLUMN IF NOT EXISTS is_public boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS ix_prompts_public ON prompts (created_at DESC) WHERE is_public;

-- == 1. Agent permissions ==

-- Back to the table of rules. The marks cannot become "always allow" rules
-- (they mean the opposite), so they go, and no connection's trusted list
-- comes back: after a Down every tool that asks first asks again.
DELETE FROM agent_ask_first_tools;
DROP INDEX ix_agent_ask_first_tools_connector_id;
ALTER TABLE agent_ask_first_tools
    DROP CONSTRAINT uq_agent_ask_first_tools_tool,
    DROP COLUMN connector_id;
ALTER TABLE agent_ask_first_tools RENAME COLUMN tool TO permission;
ALTER SEQUENCE agent_ask_first_tools_id_seq RENAME TO agent_permission_rules_id_seq;
ALTER TABLE agent_ask_first_tools RENAME CONSTRAINT agent_ask_first_tools_user_id_fkey TO agent_permission_rules_user_id_fkey;
ALTER TABLE agent_ask_first_tools RENAME CONSTRAINT agent_ask_first_tools_pkey TO agent_permission_rules_pkey;
ALTER TABLE agent_ask_first_tools RENAME TO agent_permission_rules;
ALTER TABLE agent_permission_rules
    ADD COLUMN thread_id bigint REFERENCES new_chat_threads (id) ON DELETE CASCADE,
    ADD COLUMN pattern varchar(255) NOT NULL DEFAULT '*',
    ADD COLUMN action varchar(16) NOT NULL,
    ADD CONSTRAINT ck_agent_permission_rules_action CHECK (action IN ('allow', 'deny', 'ask')),
    ADD CONSTRAINT uq_agent_permission_rules_scope
        UNIQUE NULLS NOT DISTINCT (user_id, thread_id, permission, pattern, action);
CREATE INDEX ix_agent_permission_rules_thread_id ON agent_permission_rules (thread_id);
