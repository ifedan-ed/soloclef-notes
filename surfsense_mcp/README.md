# SurfSense MCP Server

A [Model Context Protocol](https://modelcontextprotocol.io/) server that exposes
SurfSense to MCP clients like **Claude Code**, **Cursor**, and **Claude Desktop**.
It talks to a SurfSense backend purely over its REST API using a SurfSense API
key — it imports no backend code.

Connect it two ways:

- **Hosted** (recommended) — point your client at `https://mcp.surfsense.com/mcp`
  and pass your API key in a header. Nothing to install or keep running.
- **Self-host (stdio)** — run the server yourself against any backend (cloud or
  your own). Best for self-hosters and clients without remote-server support.

## Tools

**Search-space selector**
- `soloclef_list_workspaces` — list the workspaces (search spaces) you can access
- `soloclef_select_workspace` — pick the active workspace by name or id

**Scrapers (all platforms)**
- `soloclef_web_crawl`, `soloclef_google_search`, `soloclef_reddit_scrape`,
  `soloclef_youtube_scrape`, `soloclef_youtube_comments`,
  `soloclef_instagram_scrape`, `soloclef_instagram_details`,
  `soloclef_tiktok_scrape`, `soloclef_tiktok_comments`,
  `soloclef_tiktok_user_search`, `soloclef_tiktok_trending`,
  `soloclef_google_maps_scrape`, `soloclef_google_maps_reviews`,
  `soloclef_indeed_scrape`, `soloclef_amazon_scrape`,
  `soloclef_walmart_scrape`, `soloclef_walmart_reviews`
- `soloclef_list_scraper_runs`, `soloclef_get_scraper_run` — retrieve past
  results in full (useful when a large result was truncated inline)

**Knowledge base**
- `soloclef_search_knowledge_base` — semantic + keyword search over stored content
- `soloclef_list_documents`, `soloclef_get_document`
- `soloclef_add_document`, `soloclef_upload_file`
- `soloclef_update_document`, `soloclef_delete_document`

Workspace-scoped tools default to the active workspace; pass `workspace` (a name
or id) to override for a single call. Ids never need to be typed by hand — the
model carries them between calls.

## Get an API key

1. Settings (avatar menu) → **API Access**: create a personal key (`ss_pat_…`).
   It is shown only once. Any signed-in account can do this; the key acts as
   that account and nobody else.
2. Workspace settings → **General** → turn **API key access** on for each
   workspace the agent should see. It is off for every new workspace, and a
   key lists no workspace until it is on.

On a Soloclef Notes server the address is `https://<that server>/mcp` (the
web app's own host); "Connect your agent" in the avatar menu prints the config
with that address.

## Sign in from claude.ai or ChatGPT

claude.ai (and Claude Desktop/mobile) and ChatGPT cannot send an API key, so
the remote server also accepts an Authentik sign-in (the MCP authorization
spec), when `MCP_OAUTH_ISSUER` is set:

- A request without a credential gets `401` with
  `WWW-Authenticate: Bearer resource_metadata=".../.well-known/oauth-protected-resource/mcp"`.
  That document (RFC 9728, served here at both
  `/.well-known/oauth-protected-resource/mcp` and
  `/.well-known/oauth-protected-resource`) names Authentik as the only
  authorization server.
- A bearer token that is not an `ss_pat_` key is checked with the backend's
  `GET /verify-token` before the request goes on (`core/auth/sign_in_check.py`;
  a "yes" is remembered for up to 60 s, never past the token's expiry, at most
  4,096 tokens). Bad or expired (the backend answers `401`): `401
  invalid_token` with the challenge; a genuine sign-in with no Notes account,
  or one Authentik no longer lets into Notes: `403`; backend unreachable (10 s
  timeout) or any other answer, such as a 5xx or the backend's `503` while
  Authentik cannot be read: `503`, never a sign-in prompt.
- API keys are untouched: they pass straight through and the backend checks
  them on every call.

Environment (remote transport only): `MCP_OAUTH_ISSUER` (the Authentik issuer,
trailing slash included), `SURFSENSE_MCP_PUBLIC_URL` (default
`${SURFSENSE_PUBLIC_URL}/mcp`; must be the URL people paste),
`MCP_OAUTH_SCOPES` (default `openid email profile offline_access`). The
person pastes the Authentik client ID and secret into their connector; see
"AI agents sign in with Authentik" in `../custom/README.md`.

## Connect (hosted)

Point your client at the hosted server and send the key as a Bearer token. For
clients that read an `mcpServers` map (Cursor, Claude Desktop, and others):

```json
{
  "mcpServers": {
    "surfsense": {
      "url": "https://mcp.surfsense.com/mcp",
      "headers": { "Authorization": "Bearer ss_pat_your_key_here" }
    }
  }
}
```

Claude Code, from a terminal:

```bash
claude mcp add --transport http surfsense https://mcp.surfsense.com/mcp \
  --header "Authorization: Bearer ss_pat_your_key_here"
```

Most MCP clients accept this `url` + `headers` form; check your client's docs for
its exact remote-server field.

## Self-host (stdio)

Run the server yourself when you host your own backend or use a client without
remote support. It uses [uv](https://github.com/astral-sh/uv):

```bash
cd surfsense_mcp
uv sync
uv run python -m mcp_server.selfcheck   # verify tools register correctly
```

Then add it to your client. Cursor (`~/.cursor/mcp.json` or a project
`.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "surfsense": {
      "command": "uv",
      "args": ["run", "--directory", "/absolute/path/to/SurfSense/surfsense_mcp", "python", "-m", "mcp_server"],
      "env": {
        "SURFSENSE_BASE_URL": "http://localhost:8000",
        "SURFSENSE_API_KEY": "ss_pat_your_token_here"
      }
    }
  }
}
```

Claude Code:

```bash
claude mcp add surfsense \
  -e SURFSENSE_BASE_URL=http://localhost:8000 \
  -e SURFSENSE_API_KEY=ss_pat_your_token_here \
  -- uv run --directory /absolute/path/to/SurfSense/surfsense_mcp python -m mcp_server
```

Claude Desktop: add the same `mcpServers` block as Cursor to
`claude_desktop_config.json` (Settings → Developer → Edit Config).

## Configuration

See `.env.example`. For self-host, secrets are passed as environment variables by
the client; never commit tokens.

## Backend dependency

`soloclef_search_knowledge_base` calls `POST /api/v1/documents/search-semantic`,
a thin endpoint that exposes the backend's existing hybrid retriever over REST.
All other tools use pre-existing SurfSense endpoints.
