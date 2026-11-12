-- +goose Up

-- The baseline: the whole schema of the Go version of Soloclef, written fresh
-- for an empty database. There is no data migration behind it and no earlier
-- revision; the Python version's database stays where it is, as an archive.
--
-- The rules every table here follows:
--
--   * One owner. Every row a person owns carries
--     `user_id uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE`. There
--     are no workspaces, members, roles or invites, so access is one
--     sentence: a row is yours or it does not exist. Every lookup is
--     `WHERE id = $1 AND user_id = $2`.
--   * A child that carries the owner references its parent by
--     (parent id, user_id), never by the parent id alone, so the database
--     itself refuses a chunk, a file, a source or a note that points at
--     another account's row. That is why the parents have a second, composite
--     unique key beside their primary key.
--   * Ids are `bigint` identity columns (an account's id is a uuid). The
--     sequences keep the names `<table>_id_seq`.
--   * The database mints `created_at`, and `updated_at` where a table has
--     one. An application-supplied value still wins.
--   * `jsonb`, never `json`: a `json` column stores a JSON `null` and SQL NULL
--     as two different things, and every reader had to tolerate both.
--   * An index exists because a query or a cascade needs it. Every foreign
--     key that cascades or sets NULL has an index its lookup can use.
--   * No vectors. Qdrant is the only vector store; the indexer writes it
--     directly, so there is no embedding column, no pgvector and no outbox.
--
-- sqlc reads this file as the schema every query is generated against
-- (soloclef_api/sqlc.yaml), so a column renamed here fails at generate time.

-- `similarity()` and the trigram index behind the document title search.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ── Types ────────────────────────────────────────────────────────────────
--
-- The enum types the Go queries already cast to and from. They are kept so
-- that converting a package to this schema is a change of owner column and
-- not also a change of every cast. A new closed set of values is a text
-- column with a CHECK instead (see `connections.scope`, `notes.origin`),
-- which can be extended inside a transaction.

CREATE TYPE documenttype AS ENUM (
    'EXTENSION', 'CRAWLED_URL', 'FILE', 'YOUTUBE_VIDEO', 'GOOGLE_DRIVE_FILE',
    'NOTE', 'ARTIFACT', 'LOCAL_FOLDER_FILE'
);
CREATE TYPE document_file_kind AS ENUM ('ORIGINAL', 'REDACTED', 'FILLED_FORM');
CREATE TYPE searchsourceconnectortype AS ENUM (
    'GOOGLE_DRIVE_CONNECTOR', 'WEBCRAWLER_CONNECTOR', 'MCP_CONNECTOR',
    'NEXTCLOUD_CONNECTOR', 'EMAIL_IMAP_CONNECTOR'
);
CREATE TYPE modelsource AS ENUM ('DISCOVERED', 'MANUAL');
CREATE TYPE newchatmessagerole AS ENUM ('USER', 'ASSISTANT', 'SYSTEM');
CREATE TYPE artifact_file_role AS ENUM ('primary', 'preview');
CREATE TYPE deliverable_job_status AS ENUM (
    'queued', 'running', 'cancelling', 'cancelled', 'ready', 'failed'
);
CREATE TYPE podcast_status AS ENUM (
    'pending', 'awaiting_brief', 'drafting', 'awaiting_review', 'rendering',
    'ready', 'failed', 'cancelled'
);
CREATE TYPE video_presentation_status AS ENUM ('pending', 'generating', 'ready', 'failed');
CREATE TYPE automation_status AS ENUM ('active', 'paused', 'archived');
CREATE TYPE automation_trigger_type AS ENUM ('schedule', 'event', 'manual');
CREATE TYPE automation_run_status AS ENUM (
    'pending', 'running', 'succeeded', 'failed', 'needs_attention',
    'cancelled', 'timed_out'
);
CREATE TYPE prompt_mode AS ENUM ('transform', 'explore');
CREATE TYPE loglevel AS ENUM ('DEBUG', 'INFO', 'WARNING', 'ERROR', 'CRITICAL');
CREATE TYPE logstatus AS ENUM ('IN_PROGRESS', 'SUCCESS', 'FAILED');

-- ── Accounts and sign-in ─────────────────────────────────────────────────

-- An account, and the settings that are the account's own. What used to be
-- set per workspace is here, once: the memory the chat keeps about the
-- person, their standing instructions to the chat, whether answers carry
-- citations, and whether API keys and agents may act for the account.
CREATE TABLE "user" (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email              varchar(320) NOT NULL,
    -- NULL for an account that signs in only through an identity provider.
    -- No placeholder hash is stored for a password nobody has.
    hashed_password    varchar(1024),
    is_active          boolean NOT NULL DEFAULT true,
    is_superuser       boolean NOT NULL DEFAULT false,
    is_verified        boolean NOT NULL DEFAULT false,
    display_name       varchar,
    avatar_url         varchar,
    last_login         timestamptz,
    -- The chat's memory of the person, as Markdown. Empty, never NULL: there
    -- is one way to say "nothing remembered".
    memory_md          text NOT NULL DEFAULT '',
    -- Standing instructions for every chat. A notebook's own instructions
    -- still win inside that notebook.
    chat_instructions  text NOT NULL DEFAULT '',
    citations_enabled  boolean NOT NULL DEFAULT true,
    -- The one access control kept: an API key or an agent's token acts for
    -- this account only after the person has switched this on. An agent's
    -- token is issued by the identity provider, so nothing the account did
    -- stands behind it until this is true. Checked once, in the gate.
    api_access_enabled boolean NOT NULL DEFAULT false,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
-- Sign-in looks an address up in lower case, and two accounts must not
-- differ only by the case of their address.
CREATE UNIQUE INDEX uq_user_email_lower ON "user" (lower(email));

-- The link between an account and its identity at a sign-in provider: which
-- provider, which subject there, which account here. The provider's access
-- and refresh tokens are deliberately not stored: sign-in needs the link and
-- never the tokens, and a token that is not stored cannot leak.
CREATE TABLE oauth_account (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    oauth_name    varchar(100) NOT NULL,
    account_id    varchar(320) NOT NULL,
    account_email varchar(320) NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- One subject at one provider is one account here; also the sign-in lookup.
    CONSTRAINT uq_oauth_account_provider_subject UNIQUE (oauth_name, account_id)
);
CREATE INDEX ix_oauth_account_user_id ON oauth_account (user_id);

-- A browser's or the phone app's refresh token, stored as a hash. Tokens
-- rotate inside a family, so one stolen token revokes the whole family.
CREATE TABLE refresh_tokens (
    id              bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id         uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    token_hash      varchar(64) NOT NULL,
    expires_at      timestamptz NOT NULL,
    revoked_at      timestamptz,
    absolute_expiry timestamptz,
    family_id       uuid NOT NULL,
    kind            varchar(16) NOT NULL DEFAULT 'web',
    device_label    varchar(100),
    signed_in_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_refresh_tokens_token_hash UNIQUE (token_hash),
    CONSTRAINT ck_refresh_tokens_kind CHECK (kind IN ('web', 'app'))
);
CREATE INDEX ix_refresh_tokens_user_id ON refresh_tokens (user_id);
CREATE INDEX ix_refresh_tokens_family_id ON refresh_tokens (family_id);
-- The sweep of expired tokens.
CREATE INDEX ix_refresh_tokens_expires_at ON refresh_tokens (expires_at);

-- An API key a person made, stored as a hash with a short visible prefix.
-- It works only while the account's `api_access_enabled` is on.
CREATE TABLE personal_access_tokens (
    id           bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    token_hash   varchar(64) NOT NULL,
    token_prefix varchar(16) NOT NULL,
    label        varchar NOT NULL,
    expires_at   timestamptz,
    last_used_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_personal_access_tokens_token_hash UNIQUE (token_hash)
);
CREATE INDEX ix_personal_access_tokens_user_id ON personal_access_tokens (user_id);

-- Which model an account uses for what, by the name the model proxy knows
-- it by. One row per purpose. It replaces three integer columns that used 0
-- to mean "not chosen", two side tables, and a table the API created for
-- itself at start-up. No row means the deployment's configured model.
CREATE TABLE user_models (
    user_id    uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    purpose    text NOT NULL,
    model      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, purpose),
    -- A new purpose is a migration that extends this list, so no code can
    -- store a choice nothing reads.
    CONSTRAINT ck_user_models_purpose CHECK (purpose IN ('chat')),
    CONSTRAINT ck_user_models_model CHECK (model <> '')
);

-- Settings of the whole site that the operator changes without a restart,
-- one JSON value per key. The first is `mcp_allowed_hosts`, the hosts a
-- person's MCP server may be on.
CREATE TABLE site_settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- ── Push ─────────────────────────────────────────────────────────────────

-- A phone that asked for notifications, by its push token.
CREATE TABLE push_devices (
    id                bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id           uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    token             varchar(255) NOT NULL,
    platform          varchar(16) NOT NULL,
    label             varchar(120),
    language          varchar(16),
    -- The sign-in session the device registered under, so signing that
    -- session out stops its notifications.
    session_family_id uuid,
    last_seen_at      timestamptz NOT NULL DEFAULT now(),
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_push_devices_token UNIQUE (token)
);
CREATE INDEX ix_push_devices_user_id ON push_devices (user_id);
CREATE INDEX ix_push_devices_session_family_id ON push_devices (session_family_id);

-- Which kinds of notification an account wants pushed.
CREATE TABLE push_preferences (
    user_id    uuid PRIMARY KEY REFERENCES "user" (id) ON DELETE CASCADE,
    choices    jsonb NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- A push the provider accepted and has not yet reported on. The receipt
-- check reads these and removes the tokens of devices that are gone.
CREATE TABLE push_tickets (
    id         bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    ticket_id  varchar(64) NOT NULL,
    token      varchar(255) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_push_tickets_ticket_id UNIQUE (ticket_id)
);
CREATE INDEX ix_push_tickets_created_at ON push_tickets (created_at);

-- ── Models and connectors ────────────────────────────────────────────────

-- A model provider connection: the operator's (`global`) or one account's
-- own key (`user`). There is no workspace scope.
CREATE TABLE connections (
    id         bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    provider   varchar(100) NOT NULL,
    base_url   varchar(500),
    api_key    varchar,
    extra      jsonb NOT NULL DEFAULT '{}',
    scope      text NOT NULL,
    enabled    boolean NOT NULL DEFAULT true,
    user_id    uuid REFERENCES "user" (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_connections_scope CHECK (scope IN ('global', 'user')),
    -- The operator's connection has no owner and a person's has one.
    CONSTRAINT ck_connections_scope_owner CHECK ((scope = 'global') = (user_id IS NULL))
);
CREATE INDEX ix_connections_user_id ON connections (user_id);

-- A model reachable through a connection, with what it can do.
CREATE TABLE models (
    id                        bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    connection_id             bigint NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    model_id                  varchar(255) NOT NULL,
    display_name              varchar(255),
    source                    modelsource NOT NULL DEFAULT 'DISCOVERED',
    supports_chat             boolean,
    max_input_tokens          integer,
    supports_image_input      boolean,
    supports_tools            boolean,
    supports_image_generation boolean,
    capabilities_override     jsonb NOT NULL DEFAULT '{}',
    enabled                   boolean NOT NULL DEFAULT true,
    catalog                   jsonb NOT NULL DEFAULT '{}',
    created_at                timestamptz NOT NULL DEFAULT now(),
    -- Also the index for "the models of this connection".
    CONSTRAINT uq_models_connection_model_id UNIQUE (connection_id, model_id)
);

-- A source of documents or tools an account connected: a Drive, a Nextcloud,
-- a mailbox, a web crawl, an MCP server. Each belongs to the account whose
-- credentials it holds. What was a second table of per-workspace switches
-- and schedules is these columns: with one owner there is one of each.
CREATE TABLE search_source_connectors (
    id                         bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id                    uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    name                       varchar(100) NOT NULL,
    connector_type             searchsourceconnectortype NOT NULL,
    -- Off keeps the connector and its credentials but the chat and the
    -- schedule leave it alone.
    enabled                    boolean NOT NULL DEFAULT true,
    is_indexable               boolean NOT NULL DEFAULT false,
    last_indexed_at            timestamptz,
    -- Holds credentials. Never published to Zero and never selected whole by
    -- a list query.
    config                     jsonb NOT NULL,
    enable_vision_llm          boolean NOT NULL DEFAULT false,
    periodic_indexing_enabled  boolean NOT NULL DEFAULT false,
    indexing_frequency_minutes integer,
    next_scheduled_at          timestamptz,
    -- The parser this connector's files are read with. NULL means the
    -- deployment's configured parser.
    parser                     varchar(32),
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    -- Also the index for "this account's connectors".
    CONSTRAINT uq_search_source_connectors_owner_type_name UNIQUE (user_id, connector_type, name),
    CONSTRAINT ck_search_source_connectors_parser
        CHECK (parser IS NULL OR parser IN ('local', 'datalab', 'llamaparse', 'unstructured'))
);
-- The scheduler's read: the connectors that are due.
CREATE INDEX ix_search_source_connectors_due ON search_source_connectors (next_scheduled_at)
    WHERE periodic_indexing_enabled AND enabled;

-- ── Documents ────────────────────────────────────────────────────────────

-- The folder tree an account keeps its documents in, ordered among siblings
-- by a fractional index in `position`.
CREATE TABLE folders (
    id         bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    parent_id  bigint,
    name       varchar(255) NOT NULL,
    "position" varchar(50) NOT NULL,
    metadata   jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- The target of every (folder, owner) reference.
    CONSTRAINT uq_folders_id_owner UNIQUE (id, user_id),
    -- Two siblings cannot share a name, at the top level included: NULL
    -- parents are compared as equal. Also the index for "the folders of this
    -- account under this parent".
    CONSTRAINT uq_folders_owner_parent_name UNIQUE NULLS NOT DISTINCT (user_id, parent_id, name),
    -- A folder's parent is the same account's folder.
    CONSTRAINT folders_parent_owner_fkey FOREIGN KEY (parent_id, user_id)
        REFERENCES folders (id, user_id) ON DELETE CASCADE
);
CREATE INDEX ix_folders_parent_id ON folders (parent_id);

-- Everything an account can search, cite and open: an uploaded file, a web
-- page, a note, a generated artifact. `content` is the Markdown the chunks
-- are cut from. The row is the truth; vectors live in Qdrant only.
CREATE TABLE documents (
    id                       bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id                  uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    title                    varchar NOT NULL,
    document_type            documenttype NOT NULL,
    document_metadata        jsonb,
    content                  text NOT NULL,
    content_hash             varchar NOT NULL,
    -- What makes two arrivals the same document (the same upload, the same
    -- remote file), so a second arrival updates and does not duplicate.
    -- Unique per account, below: the same file may be two accounts' document.
    unique_identifier_hash   varchar,
    path                     varchar,
    source_markdown          text,
    content_needs_reindexing boolean NOT NULL DEFAULT false,
    folder_id                bigint,
    connector_id             bigint REFERENCES search_source_connectors (id) ON DELETE SET NULL,
    -- {"state": "ready" | "pending" | "processing" | "failed" | "deleting", ...}
    -- with the stage and reason beside it. Clients read it as JSON, so it
    -- stays JSON, but it is always an object that names a state.
    status                   jsonb NOT NULL DEFAULT '{"state": "ready"}',
    -- The parser this document is read with. NULL means the deployment's
    -- configured parser.
    parser                   varchar(32),
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz,
    -- The target of every (document, owner) reference: chunks, files,
    -- notebook sources, notes and artifacts all point here.
    CONSTRAINT uq_documents_id_owner UNIQUE (id, user_id),
    CONSTRAINT uq_documents_owner_identity UNIQUE (user_id, unique_identifier_hash),
    CONSTRAINT ck_documents_status
        -- COALESCE, because a CHECK passes on NULL and a status with no state
        -- has no type to compare.
        CHECK (jsonb_typeof(status) = 'object' AND COALESCE(jsonb_typeof(status -> 'state'), '') = 'string'),
    CONSTRAINT ck_documents_parser
        CHECK (parser IS NULL OR parser IN ('local', 'datalab', 'llamaparse', 'unstructured')),
    -- A document's folder is the same account's folder. Deleting the folder
    -- leaves the document, loose; only folder_id is cleared, never the owner.
    CONSTRAINT documents_folder_owner_fkey FOREIGN KEY (folder_id, user_id)
        REFERENCES folders (id, user_id) ON DELETE SET NULL (folder_id)
);
-- A folder's documents, and the folder foreign key's lookup.
CREATE INDEX ix_documents_owner_folder ON documents (user_id, folder_id);
-- The counts by type and the lists of one type (notes, artifacts).
CREATE INDEX ix_documents_owner_type ON documents (user_id, document_type);
-- "Is this content here already", asked within one account.
CREATE INDEX ix_documents_owner_content_hash ON documents (user_id, content_hash);
CREATE INDEX ix_documents_connector_id ON documents (connector_id);
-- The title search: `similarity(title, $1)` and `title ILIKE`.
CREATE INDEX ix_documents_title_trgm ON documents USING gin (title gin_trgm_ops);

-- A passage of a document: what the chat retrieves and a citation points at.
-- The id is also the id of the passage's point in Qdrant. `user_id` repeats
-- the document's owner so that the keyword search filters on the row it
-- ranks, and the composite key below is what keeps the copy honest.
CREATE TABLE chunks (
    id          bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    document_id bigint NOT NULL,
    user_id     uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    content     text NOT NULL,
    "position"  integer NOT NULL DEFAULT 0,
    start_line  integer,
    end_line    integer,
    page_start  integer,
    page_end    integer,
    created_at  timestamptz NOT NULL DEFAULT now(),
    -- A chunk cannot name one account and a document of another: the pair
    -- must exist on `documents`.
    CONSTRAINT chunks_document_owner_fkey FOREIGN KEY (document_id, user_id)
        REFERENCES documents (id, user_id) ON UPDATE CASCADE ON DELETE CASCADE
);
-- A document's chunks in reading order, and the foreign key's lookup.
CREATE INDEX ix_chunks_document_position ON chunks (document_id, "position");
CREATE INDEX ix_chunks_user_id ON chunks (user_id);
-- The keyword half of search. The expression is the one the query ranks by,
-- to the letter; a different spelling there would not use this index.
CREATE INDEX ix_chunks_content_search ON chunks USING gin (to_tsvector('english', content));

-- A stored file that belongs to a document: the uploaded original, and the
-- redacted or filled copies made from it. The bytes are in the file store,
-- under `storage_key`, in the backend the row names.
CREATE TABLE document_files (
    id                bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    document_id       bigint NOT NULL,
    user_id           uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    kind              document_file_kind NOT NULL DEFAULT 'ORIGINAL',
    storage_backend   varchar(32) NOT NULL,
    storage_key       varchar NOT NULL,
    original_filename varchar NOT NULL,
    mime_type         varchar,
    size_bytes        bigint NOT NULL,
    checksum_sha256   varchar(64),
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT document_files_document_owner_fkey FOREIGN KEY (document_id, user_id)
        REFERENCES documents (id, user_id) ON DELETE CASCADE
);
CREATE INDEX ix_document_files_document_kind ON document_files (document_id, kind);
CREATE INDEX ix_document_files_user_id ON document_files (user_id);

-- An earlier text of a document, kept so that a save can be undone by a
-- person. Numbered per document.
CREATE TABLE document_versions (
    id              bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    document_id     bigint NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    version_number  integer NOT NULL,
    source_markdown text,
    content_hash    varchar NOT NULL,
    title           varchar,
    created_at      timestamptz NOT NULL DEFAULT now(),
    -- Also the index for "this document's versions, in order".
    CONSTRAINT uq_document_versions_number UNIQUE (document_id, version_number)
);

-- A short summary of a document, shown beside it as a notebook source. It
-- is a cache: `content_hash` says which text it summarises, and a document
-- whose hash has moved on is summarised again.
CREATE TABLE document_source_guides (
    document_id  bigint PRIMARY KEY REFERENCES documents (id) ON DELETE CASCADE,
    content_hash varchar(128) NOT NULL,
    summary      text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- ── Notebooks ────────────────────────────────────────────────────────────

-- A notebook: a set of sources with its own chat, studio outputs, notes and
-- chat settings.
CREATE TABLE notebooks (
    id                    bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id               uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    title                 varchar(200) NOT NULL,
    icon                  varchar(16),
    -- Who chose the title and the icon. An automatic name is replaced when
    -- the sources change; a person's is never touched.
    title_source          varchar(8) NOT NULL DEFAULT 'user',
    icon_source           varchar(8) NOT NULL DEFAULT 'user',
    auto_title            jsonb,
    focus_suggestions     jsonb,
    focus_suggestions_key varchar(64),
    chat_goal             varchar(16) NOT NULL DEFAULT 'default',
    chat_instructions     text,
    response_length       varchar(16) NOT NULL DEFAULT 'default',
    -- When the owner pinned the notebook to the top of their list, or NULL.
    -- A column and not a (notebook, user) table: a notebook has one owner.
    featured_at           timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    -- The target of every (notebook, owner) reference.
    CONSTRAINT uq_notebooks_id_owner UNIQUE (id, user_id),
    CONSTRAINT ck_notebooks_chat_goal CHECK (chat_goal IN ('default', 'learning_guide', 'custom')),
    CONSTRAINT ck_notebooks_response_length CHECK (response_length IN ('default', 'longer', 'shorter')),
    CONSTRAINT ck_notebooks_title_source CHECK (title_source IN ('auto', 'user')),
    CONSTRAINT ck_notebooks_icon_source CHECK (icon_source IN ('auto', 'user'))
);
-- An account's notebooks, most recently used first.
CREATE INDEX ix_notebooks_owner_updated ON notebooks (user_id, updated_at DESC);

-- A document or a folder a notebook reads from, and whether it is ticked.
-- This is also how a note becomes readable by a notebook's chat: the note's
-- document is added here, and nothing is copied. The owner is repeated so
-- the three composite keys can refuse a source from another account, which
-- a stale row could once link.
CREATE TABLE notebook_sources (
    id          bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    notebook_id bigint NOT NULL,
    user_id     uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    document_id bigint,
    folder_id   bigint,
    enabled     boolean NOT NULL DEFAULT true,
    added_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_notebook_sources_one_target CHECK (num_nonnulls(document_id, folder_id) = 1),
    -- Also the index for "this notebook's sources".
    CONSTRAINT uq_notebook_sources_document UNIQUE (notebook_id, document_id),
    CONSTRAINT uq_notebook_sources_folder UNIQUE (notebook_id, folder_id),
    CONSTRAINT notebook_sources_notebook_owner_fkey FOREIGN KEY (notebook_id, user_id)
        REFERENCES notebooks (id, user_id) ON DELETE CASCADE,
    CONSTRAINT notebook_sources_document_owner_fkey FOREIGN KEY (document_id, user_id)
        REFERENCES documents (id, user_id) ON DELETE CASCADE,
    CONSTRAINT notebook_sources_folder_owner_fkey FOREIGN KEY (folder_id, user_id)
        REFERENCES folders (id, user_id) ON DELETE CASCADE
);
CREATE INDEX ix_notebook_sources_document_id ON notebook_sources (document_id);
CREATE INDEX ix_notebook_sources_folder_id ON notebook_sources (folder_id);
CREATE INDEX ix_notebook_sources_user_id ON notebook_sources (user_id);

-- An account's edit of a built-in report template, used inside any of its
-- notebooks. No row means the built-in text.
CREATE TABLE notebook_report_templates (
    id           bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    template_key varchar(64) NOT NULL,
    title        varchar(200) NOT NULL,
    description  varchar(500) NOT NULL DEFAULT '',
    prompt       text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_notebook_report_templates_key UNIQUE (user_id, template_key)
);

-- ── Chat ─────────────────────────────────────────────────────────────────

-- A conversation. It is its owner's and nobody else's: there is no
-- visibility column because there is nobody to share with. A public link is
-- a snapshot (`public_chat_snapshots`), not access to the thread.
CREATE TABLE new_chat_threads (
    id                      bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id                 uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    title                   varchar(500) NOT NULL,
    archived                boolean NOT NULL DEFAULT false,
    -- The notebook this is the chat of, or NULL for a plain chat.
    notebook_id             bigint,
    -- Set when the thread was started from somebody's public link.
    cloned_from_thread_id   bigint REFERENCES new_chat_threads (id) ON DELETE SET NULL,
    cloned_from_snapshot_id bigint,
    cloned_at               timestamptz,
    needs_history_bootstrap boolean NOT NULL DEFAULT false,
    source                  text NOT NULL DEFAULT 'surfsense',
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    -- The target of every (thread, owner) reference.
    CONSTRAINT uq_new_chat_threads_id_owner UNIQUE (id, user_id),
    -- A notebook's chat is in the same account's notebook. Deleting the
    -- notebook leaves the conversation as a plain chat.
    CONSTRAINT new_chat_threads_notebook_owner_fkey FOREIGN KEY (notebook_id, user_id)
        REFERENCES notebooks (id, user_id) ON DELETE SET NULL (notebook_id)
);
-- An account's conversations, most recent first.
CREATE INDEX ix_new_chat_threads_owner_updated ON new_chat_threads (user_id, updated_at DESC);
CREATE INDEX ix_new_chat_threads_notebook_id ON new_chat_threads (notebook_id);
CREATE INDEX ix_new_chat_threads_cloned_from_thread_id ON new_chat_threads (cloned_from_thread_id);
CREATE INDEX ix_new_chat_threads_cloned_from_snapshot_id ON new_chat_threads (cloned_from_snapshot_id);

-- One message of a conversation: its role and its parts, as the clients
-- render them. Owned through its thread.
CREATE TABLE new_chat_messages (
    id                bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    thread_id         bigint NOT NULL REFERENCES new_chat_threads (id) ON DELETE CASCADE,
    role              newchatmessagerole NOT NULL,
    content           jsonb NOT NULL,
    author_id         uuid REFERENCES "user" (id) ON DELETE SET NULL,
    -- The turn the message belongs to: a question and its answer share one.
    turn_id           varchar(64),
    source            text NOT NULL DEFAULT 'surfsense',
    platform_metadata jsonb,
    created_at        timestamptz NOT NULL DEFAULT now()
);
-- A thread's messages in order.
CREATE INDEX ix_new_chat_messages_thread_created ON new_chat_messages (thread_id, created_at, id);
CREATE INDEX ix_new_chat_messages_author_id ON new_chat_messages (author_id);
-- A turn has one message per role, so a retried save cannot write the
-- answer twice.
CREATE UNIQUE INDEX uq_new_chat_messages_thread_turn_role
    ON new_chat_messages (thread_id, turn_id, role) WHERE turn_id IS NOT NULL;

-- Whether the assistant is answering in a thread right now, so every open
-- tab shows it. One row per thread.
CREATE TABLE chat_session_state (
    id                       bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    thread_id                bigint NOT NULL REFERENCES new_chat_threads (id) ON DELETE CASCADE,
    ai_responding_to_user_id uuid REFERENCES "user" (id) ON DELETE SET NULL,
    updated_at               timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_chat_session_state_thread UNIQUE (thread_id)
);
CREATE INDEX ix_chat_session_state_responding ON chat_session_state (ai_responding_to_user_id);

-- A notebook chat that was set aside when the notebook's chat was started
-- afresh. It is listed under "earlier chats" and can be reopened.
CREATE TABLE notebook_earlier_chats (
    thread_id   bigint PRIMARY KEY REFERENCES new_chat_threads (id) ON DELETE CASCADE,
    notebook_id bigint NOT NULL,
    user_id     uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    moved_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notebook_earlier_chats_notebook_owner_fkey FOREIGN KEY (notebook_id, user_id)
        REFERENCES notebooks (id, user_id) ON DELETE CASCADE
);
CREATE INDEX ix_notebook_earlier_chats_notebook ON notebook_earlier_chats (notebook_id, moved_at DESC);
CREATE INDEX ix_notebook_earlier_chats_user_id ON notebook_earlier_chats (user_id);

-- The agent loop's saved state for a thread, so a turn that waits for an
-- approval, or is cut off by a restart, resumes where it stopped. One row
-- per thread, replaced on every save. It belongs to soloclef_agent
-- (checkpoint/postgres), which keys it by a name ("chat-<thread id>") and
-- knows nothing of accounts. The columns are that package's, to the letter.
--
-- `state` is bytea although it holds JSON: jsonb refuses a string
-- containing U+0000, and tool output contains one often enough.
CREATE TABLE soloclef_agent_checkpoints (
    thread_id  text        PRIMARY KEY,
    seq        bigint      NOT NULL,
    status     text        NOT NULL,
    node       text        NOT NULL,
    state      bytea       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- Makes "every checkpoint whose name starts with this" a range scan. The
-- primary key's index is in the database's collation, which a prefix
-- comparison can use only when that collation is "C".
CREATE INDEX soloclef_agent_checkpoints_tree
    ON soloclef_agent_checkpoints (thread_id text_pattern_ops);

-- ── Studio ───────────────────────────────────────────────────────────────

-- A generated thing a person keeps: flashcards, a quiz, a mind map, a
-- report, slides, an infographic. Each is also a document (type ARTIFACT),
-- so it can be searched, cited and versioned like one.
CREATE TABLE artifacts (
    id                      bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    document_id             bigint NOT NULL,
    user_id                 uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    thread_id               bigint REFERENCES new_chat_threads (id) ON DELETE SET NULL,
    format                  varchar NOT NULL,
    -- Counts the rewrites, so a client can tell a newer payload from the
    -- one it holds.
    generation              integer NOT NULL DEFAULT 1,
    created_by_tool_call_id text,
    updated_by_tool_call_id text,
    metadata                jsonb,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_artifacts_document_id UNIQUE (document_id),
    CONSTRAINT ck_artifacts_generation_positive CHECK (generation > 0),
    CONSTRAINT artifacts_document_owner_fkey FOREIGN KEY (document_id, user_id)
        REFERENCES documents (id, user_id) ON DELETE CASCADE
);
-- An account's artifacts, newest first.
CREATE INDEX ix_artifacts_owner_updated ON artifacts (user_id, updated_at DESC);
CREATE INDEX ix_artifacts_thread_id ON artifacts (thread_id);

-- A stored file of an artifact: the thing itself, and its preview image.
CREATE TABLE artifact_files (
    id                bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    artifact_id       bigint NOT NULL REFERENCES artifacts (id) ON DELETE CASCADE,
    role              artifact_file_role NOT NULL,
    storage_backend   varchar(32) NOT NULL,
    storage_key       varchar NOT NULL,
    original_filename varchar NOT NULL,
    mime_type         varchar NOT NULL,
    size_bytes        bigint NOT NULL,
    checksum_sha256   varchar(64) NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    -- Also the index for "this artifact's files".
    CONSTRAINT uq_artifact_files_artifact_role UNIQUE (artifact_id, role),
    CONSTRAINT uq_artifact_files_storage_key UNIQUE (storage_key),
    CONSTRAINT ck_artifact_files_size_positive CHECK (size_bytes > 0)
);

-- A long piece of generation work (a report, slides, a video) as a job:
-- queued by the chat, claimed by a worker, heartbeating while it runs. The
-- row is the queue's outbox and the progress a client follows.
CREATE TABLE deliverable_jobs (
    id                  bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id             uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    kind                varchar(64) NOT NULL,
    title               varchar(500) NOT NULL,
    thread_id           bigint REFERENCES new_chat_threads (id) ON DELETE SET NULL,
    tool_call_id        text NOT NULL,
    request             jsonb NOT NULL DEFAULT '{}',
    checkpoint          jsonb NOT NULL DEFAULT '{}',
    status              deliverable_job_status NOT NULL DEFAULT 'queued',
    phase               varchar(64),
    progress            integer NOT NULL DEFAULT 0,
    artifact_id         bigint REFERENCES artifacts (id) ON DELETE SET NULL,
    celery_task_id      varchar(255),
    attempt_count       integer NOT NULL DEFAULT 1,
    failure_code        varchar(64),
    internal_error      varchar(2000),
    cancel_requested_at timestamptz,
    claimed_at          timestamptz,
    heartbeat_at        timestamptz,
    finished_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_deliverable_jobs_attempt_count CHECK (attempt_count >= 1),
    CONSTRAINT ck_deliverable_jobs_progress CHECK (progress BETWEEN 0 AND 100)
);
-- One job per tool call, so a turn that is retried does not start the work
-- twice. md5 because a tool call id has no length limit and an index row has.
CREATE UNIQUE INDEX uq_deliverable_jobs_owner_kind_tool_call
    ON deliverable_jobs (user_id, kind, md5(tool_call_id));
-- An account's jobs in one state.
CREATE INDEX ix_deliverable_jobs_owner_status ON deliverable_jobs (user_id, status);
-- The outbox: queued jobs, oldest first.
CREATE INDEX ix_deliverable_jobs_outbox ON deliverable_jobs (updated_at) WHERE status = 'queued';
-- The sweep for jobs whose worker died: running, with an old heartbeat.
CREATE INDEX ix_deliverable_jobs_stale_running ON deliverable_jobs (heartbeat_at, id)
    WHERE status IN ('running', 'cancelling');
CREATE INDEX ix_deliverable_jobs_thread_id ON deliverable_jobs (thread_id);
CREATE INDEX ix_deliverable_jobs_artifact_id ON deliverable_jobs (artifact_id);

-- One audio overview being made: the brief a person reviews, the
-- transcript, and where it has got to.
CREATE TABLE podcast_runs (
    id                 bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id            uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    title              varchar(500) NOT NULL,
    status             podcast_status NOT NULL DEFAULT 'pending',
    source_content     text,
    spec               jsonb,
    spec_version       integer NOT NULL DEFAULT 1,
    podcast_transcript jsonb,
    duration_seconds   integer,
    error              text,
    artifact_id        bigint REFERENCES artifacts (id) ON DELETE SET NULL,
    thread_id          bigint REFERENCES new_chat_threads (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_podcast_runs_owner_created ON podcast_runs (user_id, created_at DESC);
CREATE INDEX ix_podcast_runs_thread_id ON podcast_runs (thread_id);
CREATE INDEX ix_podcast_runs_artifact_id ON podcast_runs (artifact_id);

-- One video presentation being made, and where it has got to. The video's
-- own payload is in the artifact.
CREATE TABLE video_presentation_runs (
    id          bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    title       varchar(500) NOT NULL,
    status      video_presentation_status NOT NULL DEFAULT 'ready',
    error       text,
    thread_id   bigint REFERENCES new_chat_threads (id) ON DELETE SET NULL,
    artifact_id bigint REFERENCES artifacts (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_video_presentation_runs_owner_created ON video_presentation_runs (user_id, created_at DESC);
CREATE INDEX ix_video_presentation_runs_thread_id ON video_presentation_runs (thread_id);
CREATE INDEX ix_video_presentation_runs_artifact_id ON video_presentation_runs (artifact_id);

-- A studio output as a notebook lists it: what kind, made from which
-- sources with which options, and the run that is making or made it.
CREATE TABLE notebook_outputs (
    id                  bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    notebook_id         bigint NOT NULL,
    user_id             uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    kind                varchar(32) NOT NULL,
    title               varchar(500) NOT NULL,
    options             jsonb NOT NULL DEFAULT '{}',
    source_document_ids jsonb NOT NULL DEFAULT '[]',
    podcast_id          bigint REFERENCES podcast_runs (id) ON DELETE SET NULL,
    deliverable_job_id  bigint REFERENCES deliverable_jobs (id) ON DELETE SET NULL,
    thread_id           bigint REFERENCES new_chat_threads (id) ON DELETE SET NULL,
    generated_by        jsonb,
    created_at          timestamptz NOT NULL DEFAULT now(),
    -- An output is made by one run, of one of the two kinds, or by none.
    CONSTRAINT ck_notebook_outputs_one_run CHECK (num_nonnulls(podcast_id, deliverable_job_id) <= 1),
    CONSTRAINT notebook_outputs_notebook_owner_fkey FOREIGN KEY (notebook_id, user_id)
        REFERENCES notebooks (id, user_id) ON DELETE CASCADE
);
-- A notebook's outputs, newest first.
CREATE INDEX ix_notebook_outputs_notebook_created ON notebook_outputs (notebook_id, created_at DESC);
CREATE INDEX ix_notebook_outputs_user_id ON notebook_outputs (user_id);
CREATE INDEX ix_notebook_outputs_podcast_id ON notebook_outputs (podcast_id);
CREATE INDEX ix_notebook_outputs_deliverable_job_id ON notebook_outputs (deliverable_job_id);
CREATE INDEX ix_notebook_outputs_thread_id ON notebook_outputs (thread_id);

-- ── Public links ─────────────────────────────────────────────────────────

-- A read-only public link to a whole notebook. The token is the whole
-- authorisation: whoever holds it may read, until it expires or is revoked.
CREATE TABLE notebook_public_links (
    id          bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    notebook_id bigint NOT NULL,
    user_id     uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    share_token varchar(128) NOT NULL,
    expires_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    -- A notebook has at most one link.
    CONSTRAINT uq_notebook_public_links_notebook UNIQUE (notebook_id),
    CONSTRAINT uq_notebook_public_links_share_token UNIQUE (share_token),
    CONSTRAINT notebook_public_links_notebook_owner_fkey FOREIGN KEY (notebook_id, user_id)
        REFERENCES notebooks (id, user_id) ON DELETE CASCADE
);
CREATE INDEX ix_notebook_public_links_user_id ON notebook_public_links (user_id);

-- A frozen copy of a chat, or of one studio output, behind a public link.
-- It is a copy so that what was shared cannot change under its readers.
CREATE TABLE public_chat_snapshots (
    id                 bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id            uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    thread_id          bigint REFERENCES new_chat_threads (id) ON DELETE CASCADE,
    notebook_output_id bigint REFERENCES notebook_outputs (id) ON DELETE CASCADE,
    share_token        varchar(64) NOT NULL,
    content_hash       varchar(64) NOT NULL,
    snapshot_data      jsonb NOT NULL,
    message_ids        bigint[] NOT NULL,
    expires_at         timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_public_chat_snapshots_share_token UNIQUE (share_token),
    -- Sharing the same content twice returns the first link. Also the index
    -- for "this thread's snapshots".
    CONSTRAINT uq_snapshot_thread_content_hash UNIQUE (thread_id, content_hash),
    CONSTRAINT ck_public_snapshot_one_subject CHECK (num_nonnulls(thread_id, notebook_output_id) = 1)
);
-- "Your links".
CREATE INDEX ix_public_chat_snapshots_user_id ON public_chat_snapshots (user_id);
CREATE INDEX ix_public_chat_snapshots_notebook_output_id ON public_chat_snapshots (notebook_output_id);

-- Declared here because the two tables name each other: a thread started
-- from a public link remembers the snapshot until the snapshot is deleted.
ALTER TABLE new_chat_threads
    ADD CONSTRAINT new_chat_threads_cloned_from_snapshot_id_fkey
    FOREIGN KEY (cloned_from_snapshot_id) REFERENCES public_chat_snapshots (id) ON DELETE SET NULL;

-- ── Automations ──────────────────────────────────────────────────────────

-- A saved agent task that runs on a schedule, on an event or by hand, as
-- its owner and over its owner's documents.
CREATE TABLE automations (
    id          bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    name        varchar(200) NOT NULL,
    description text,
    status      automation_status NOT NULL DEFAULT 'active',
    definition  jsonb NOT NULL,
    version     integer NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_automations_owner_updated ON automations (user_id, updated_at DESC);

-- What starts an automation: a schedule, an event or a button.
CREATE TABLE automation_triggers (
    id            bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    automation_id bigint NOT NULL REFERENCES automations (id) ON DELETE CASCADE,
    type          automation_trigger_type NOT NULL,
    params        jsonb NOT NULL,
    static_inputs jsonb NOT NULL DEFAULT '{}',
    enabled       boolean NOT NULL DEFAULT true,
    last_fired_at timestamptz,
    next_fire_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_automation_triggers_automation_id ON automation_triggers (automation_id);
-- The scheduler's read: the triggers that are due.
CREATE INDEX ix_automation_triggers_due ON automation_triggers (next_fire_at) WHERE enabled;

-- One run of an automation: a copy of the definition it ran, each step's
-- result, and what it produced.
CREATE TABLE automation_runs (
    id                  bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    automation_id       bigint NOT NULL REFERENCES automations (id) ON DELETE CASCADE,
    trigger_id          bigint REFERENCES automation_triggers (id) ON DELETE SET NULL,
    status              automation_run_status NOT NULL DEFAULT 'pending',
    definition_snapshot jsonb NOT NULL,
    inputs              jsonb NOT NULL DEFAULT '{}',
    step_results        jsonb NOT NULL DEFAULT '[]',
    output              jsonb,
    artifacts           jsonb NOT NULL DEFAULT '[]',
    error               jsonb,
    started_at          timestamptz,
    finished_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now()
);
-- An automation's runs, newest first.
CREATE INDEX ix_automation_runs_automation_created ON automation_runs (automation_id, created_at DESC);
CREATE INDEX ix_automation_runs_trigger_id ON automation_runs (trigger_id);

-- ── Notes ────────────────────────────────────────────────────────────────

-- What makes a document a note. There is one kind of note: every note IS a
-- `documents` row of type NOTE, so it is indexed, searchable, citable and
-- versioned by the code every document already has, and the assistant can
-- read what a person writes by hand. This row holds only what a document
-- does not: the notebook it is filed in (or none), where it came from, and
-- whether it is pinned. The note's id is its document's id, so a citation
-- and a link to a note are the same number. The notes package writes both
-- rows in one transaction.
--
-- "Use as a source" is a `notebook_sources` row for the same document, not a
-- conversion, so nothing is copied and nothing drifts.
CREATE TABLE notes (
    document_id   bigint PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    -- NULL for a note in no notebook.
    notebook_id   bigint,
    -- Written by hand, kept from a chat answer, or written by an automation.
    origin        text NOT NULL DEFAULT 'hand',
    automation_id bigint REFERENCES automations (id) ON DELETE SET NULL,
    -- The answer a note was kept from, so the note links back to the
    -- conversation and the answer can show "Saved to notes".
    thread_id     bigint REFERENCES new_chat_threads (id) ON DELETE SET NULL,
    message_id    bigint REFERENCES new_chat_messages (id) ON DELETE SET NULL,
    pinned_at     timestamptz,
    CONSTRAINT ck_notes_origin CHECK (origin IN ('hand', 'chat', 'automation')),
    CONSTRAINT notes_document_owner_fkey FOREIGN KEY (document_id, user_id)
        REFERENCES documents (id, user_id) ON DELETE CASCADE,
    -- Deleting a notebook does not delete its notes: they become unfiled.
    CONSTRAINT notes_notebook_owner_fkey FOREIGN KEY (notebook_id, user_id)
        REFERENCES notebooks (id, user_id) ON DELETE SET NULL (notebook_id)
);
-- The Notes place filtered to one notebook or to "not in a notebook", and a
-- notebook's Notes tab.
CREATE INDEX ix_notes_owner_notebook ON notes (user_id, notebook_id);
-- Pinned notes stay on top; most notes are not pinned.
CREATE INDEX ix_notes_owner_pinned ON notes (user_id, pinned_at DESC) WHERE pinned_at IS NOT NULL;
CREATE INDEX ix_notes_notebook_id ON notes (notebook_id);
CREATE INDEX ix_notes_automation_id ON notes (automation_id);
CREATE INDEX ix_notes_thread_id ON notes (thread_id);
CREATE INDEX ix_notes_message_id ON notes (message_id);

-- ── The agent's edits ────────────────────────────────────────────────────

-- Every tool call the assistant made that changed something, with enough
-- to undo it. `reverse_of` marks a row that is itself the undoing of
-- another.
CREATE TABLE agent_action_log (
    id                 bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    thread_id          bigint NOT NULL REFERENCES new_chat_threads (id) ON DELETE CASCADE,
    user_id            uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    turn_id            text,
    tool_call_id       text,
    chat_turn_id       varchar(64),
    message_id         text,
    tool_name          varchar(255) NOT NULL,
    args               jsonb,
    result_id          varchar(255),
    reversible         boolean NOT NULL DEFAULT false,
    reverse_descriptor jsonb,
    error              jsonb,
    reverse_of         bigint REFERENCES agent_action_log (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now()
);
-- A thread's actions in order.
CREATE INDEX ix_agent_action_log_thread_created ON agent_action_log (thread_id, created_at);
CREATE INDEX ix_agent_action_log_owner_created ON agent_action_log (user_id, created_at DESC);
-- The Revert button finds a turn's actions by the turn.
CREATE INDEX ix_agent_action_log_chat_turn_id ON agent_action_log (chat_turn_id);
CREATE INDEX ix_agent_action_log_tool_call_id ON agent_action_log (tool_call_id);
-- An action is undone at most once.
CREATE UNIQUE INDEX uq_agent_action_log_reverse_of ON agent_action_log (reverse_of) WHERE reverse_of IS NOT NULL;

-- A standing answer to "may the assistant do this": allow, deny or ask,
-- for the whole account (thread_id NULL) or for one thread.
CREATE TABLE agent_permission_rules (
    id         bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    thread_id  bigint REFERENCES new_chat_threads (id) ON DELETE CASCADE,
    permission varchar(255) NOT NULL,
    pattern    varchar(255) NOT NULL DEFAULT '*',
    action     varchar(16) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_agent_permission_rules_action CHECK (action IN ('allow', 'deny', 'ask')),
    -- NULLS NOT DISTINCT, or an account-wide rule (thread_id NULL) could be
    -- stored any number of times: NULLs compare as different by default.
    -- Also the index for "this account's rules".
    CONSTRAINT uq_agent_permission_rules_scope
        UNIQUE NULLS NOT DISTINCT (user_id, thread_id, permission, pattern, action)
);
CREATE INDEX ix_agent_permission_rules_thread_id ON agent_permission_rules (thread_id);

-- A document as it was before the assistant changed it, so the change can
-- be undone. It outlives the document: undoing a delete needs it.
CREATE TABLE document_revisions (
    id                 bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    document_id        bigint REFERENCES documents (id) ON DELETE SET NULL,
    user_id            uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    content_before     text,
    title_before       varchar,
    folder_id_before   bigint,
    chunks_before      jsonb,
    metadata_before    jsonb,
    created_by_turn_id text,
    agent_action_id    bigint REFERENCES agent_action_log (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_document_revisions_document_id ON document_revisions (document_id);
CREATE INDEX ix_document_revisions_user_id ON document_revisions (user_id);
CREATE INDEX ix_document_revisions_agent_action_id ON document_revisions (agent_action_id);
CREATE INDEX ix_document_revisions_created_by_turn_id ON document_revisions (created_by_turn_id);

-- A folder as it was before the assistant renamed or moved it.
CREATE TABLE folder_revisions (
    id                 bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    folder_id          bigint REFERENCES folders (id) ON DELETE SET NULL,
    user_id            uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    name_before        varchar(255),
    parent_id_before   bigint,
    position_before    varchar(50),
    created_by_turn_id text,
    agent_action_id    bigint REFERENCES agent_action_log (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_folder_revisions_folder_id ON folder_revisions (folder_id);
CREATE INDEX ix_folder_revisions_user_id ON folder_revisions (user_id);
CREATE INDEX ix_folder_revisions_agent_action_id ON folder_revisions (agent_action_id);
CREATE INDEX ix_folder_revisions_created_by_turn_id ON folder_revisions (created_by_turn_id);

-- ── Records ──────────────────────────────────────────────────────────────

-- What a model call used: tokens, and the cost the proxy reported. A record
-- for the person to read, not a ledger; nothing is charged from it.
CREATE TABLE token_usage (
    id                bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id           uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    usage_type        varchar(50) NOT NULL,
    prompt_tokens     integer NOT NULL,
    completion_tokens integer NOT NULL,
    total_tokens      integer NOT NULL,
    cost_micros       bigint NOT NULL DEFAULT 0,
    model_breakdown   jsonb,
    call_details      jsonb,
    thread_id         bigint REFERENCES new_chat_threads (id) ON DELETE CASCADE,
    message_id        bigint REFERENCES new_chat_messages (id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_token_usage_owner_created ON token_usage (user_id, created_at DESC);
CREATE INDEX ix_token_usage_thread_id ON token_usage (thread_id);
-- One record per answer, so a retried save does not count a turn twice.
CREATE UNIQUE INDEX uq_token_usage_message_id ON token_usage (message_id) WHERE message_id IS NOT NULL;

-- A row of an account's inbox.
CREATE TABLE notifications (
    id         bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    type       varchar(50) NOT NULL,
    title      varchar(200) NOT NULL,
    message    text NOT NULL,
    read       boolean NOT NULL DEFAULT false,
    metadata   jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz
);
-- The inbox, newest first.
CREATE INDEX ix_notifications_owner_created ON notifications (user_id, created_at DESC);
-- The unread count, and the unread of one type.
CREATE INDEX ix_notifications_owner_read_type_created ON notifications (user_id, read, type, created_at);

-- A saved prompt: an account's own, its edit of a built-in one
-- (`default_prompt_slug`), or one it published to the community shelf.
CREATE TABLE prompts (
    id                  bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id             uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    default_prompt_slug varchar(100),
    name                varchar(200) NOT NULL,
    prompt              text NOT NULL,
    mode                prompt_mode NOT NULL,
    version             integer NOT NULL DEFAULT 1,
    is_public           boolean NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL DEFAULT now(),
    -- One edit per built-in prompt per account. Also the index for "this
    -- account's prompts".
    CONSTRAINT uq_prompt_user_default_slug UNIQUE (user_id, default_prompt_slug)
);
-- The community shelf: the few prompts that are public.
CREATE INDEX ix_prompts_public ON prompts (created_at DESC) WHERE is_public;

-- The account's activity log: what background work did and how it ended.
CREATE TABLE logs (
    id           bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    level        loglevel NOT NULL,
    status       logstatus NOT NULL,
    message      text NOT NULL,
    source       varchar(200),
    log_metadata jsonb,
    created_at   timestamptz NOT NULL DEFAULT now()
);
-- The log, newest first, and the summary of a recent window.
CREATE INDEX ix_logs_owner_created ON logs (user_id, created_at DESC);

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

-- ── Zero ─────────────────────────────────────────────────────────────────
--
-- The web app's live updates. zero-cache replicates exactly these tables,
-- and for the tables with a column list exactly these columns, to browsers.
-- Every published column of a row is sent to whoever syncs the row, so a
-- column list is a security boundary: `search_source_connectors.config`
-- holds credentials and is not in it, and "user" is not published at all.
-- Each table carries the owner (`user_id`) or reaches it through one parent
-- that is published too (a message through its thread, a run through its
-- automation), which is what the web app's Zero queries filter on.
--
-- Changing this list is a migration, and zero-cache is reset with it.
CREATE PUBLICATION zero_publication FOR TABLE
    notifications,
    folders,
    documents (id, user_id, title, document_type, folder_id, status, created_at, updated_at),
    search_source_connectors (id, user_id, name, connector_type, enabled, is_indexable,
        last_indexed_at, periodic_indexing_enabled, indexing_frequency_minutes,
        next_scheduled_at, created_at),
    new_chat_threads (id, user_id),
    new_chat_messages,
    chat_session_state,
    automations (id, user_id),
    automation_runs (id, automation_id, trigger_id, status, step_results, started_at,
        finished_at, created_at),
    podcast_runs (id, user_id, title, status, spec, spec_version, duration_seconds, error,
        artifact_id, thread_id, created_at),
    video_presentation_runs (id, user_id, title, status, error, artifact_id, thread_id, created_at),
    deliverable_jobs (id, user_id, kind, title, status, phase, progress, failure_code,
        artifact_id, thread_id, created_at, updated_at);

-- +goose Down

-- For a development database only: this removes every table and every row.
-- The migrate command refuses to run it unless it is told, by name, that
-- the database is a development one (cmd/migrate).
DROP PUBLICATION zero_publication;

DROP TABLE
    site_announcements, logs, prompts, notifications, token_usage,
    folder_revisions, document_revisions, agent_permission_rules, agent_action_log,
    notes,
    automation_runs, automation_triggers, automations,
    public_chat_snapshots, notebook_public_links, notebook_outputs,
    video_presentation_runs, podcast_runs, deliverable_jobs, artifact_files, artifacts,
    soloclef_agent_checkpoints,
    notebook_earlier_chats, chat_session_state, new_chat_messages, new_chat_threads,
    notebook_report_templates, notebook_sources, notebooks,
    document_source_guides, document_versions, document_files, chunks, documents, folders,
    search_source_connectors, models, connections,
    push_tickets, push_preferences, push_devices,
    site_settings, user_models, personal_access_tokens, refresh_tokens, oauth_account, "user"
    CASCADE;

DROP TYPE
    logstatus, loglevel, prompt_mode, automation_run_status, automation_trigger_type,
    automation_status, video_presentation_status, podcast_status, deliverable_job_status,
    artifact_file_role, newchatmessagerole, modelsource, searchsourceconnectortype,
    document_file_kind, documenttype;

DROP EXTENSION pg_trgm;
