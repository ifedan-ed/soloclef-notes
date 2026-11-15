-- +goose Up

-- An account's own key for a web search provider: Exa or Tavily.
--
-- The chat's two web search tools and the notebook research box search with
-- it, on the person's own account with the provider. Until now the only way
-- to give Soloclef such a key was to add the provider's MCP server, and the
-- key was dug out of that server's stored headers or address; a key is now a
-- connection of its own, entered once, checked against the provider before
-- it is saved, and stored sealed.
--
-- One key per provider per account, so the primary key is the pair.
-- `api_key` is sealedsecret's text under the web search key purpose, bound
-- to the owner: it is opened only for a search and never answered.
-- `key_mask` ("••••" and the last four characters) is what the settings
-- screen shows, computed when the key was sealed so that showing it opens
-- nothing.
--
-- Migration 00012 moves the keys the MCP rows held into this table.
CREATE TABLE web_search_connections (
    user_id    uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    provider   text NOT NULL,
    api_key    text NOT NULL,
    key_mask   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, provider),
    CONSTRAINT ck_web_search_connections_provider CHECK (provider IN ('exa', 'tavily'))
);

-- +goose Down

DROP TABLE web_search_connections;
