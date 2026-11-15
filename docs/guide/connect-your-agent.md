# Connect your agent

Soloclef runs an MCP server at `/mcp` on your server's address. AI agents
can use it to search and read your documents, add to them, and run the
academic searches.

- Claude and ChatGPT connect by signing in through the server's single
  sign-on, when the server has agent sign-in set up.
- Coding agents such as Claude Code, Codex and Cursor, and other MCP clients
  such as Open WebUI, connect with an API key.

Either way, the agent acts as you and can do with your documents whatever
you can.

## First, turn on API access

In Settings, API access, switch on API and agent access. It is off for a new
account, and until it is on every agent is refused.

## Claude and ChatGPT

Open your avatar menu and choose Connect your agent. The top of the dialog
shows the server's address, a Client ID and a Client secret, each with a
copy button, and the steps for each:

- Claude, on the web, desktop or phone: Customize, Connectors, Add custom
  connector. On a Team or Enterprise plan, an Owner adds it under
  Organization settings, Connectors. Enter the server address, choose Use
  your own OAuth client, and paste the client ID and secret. Then Connect and
  sign in.
- ChatGPT: under Settings, Apps & Connectors, Advanced settings, turn on
  Developer mode. Create an app with the server address and OAuth, and paste
  the client ID and secret under Advanced OAuth settings, with the token
  method `client_secret_post`. Then sign in when asked.

You sign in on the single sign-on page and approve access. Approve only a
connection you started yourself, never one from a link someone sent you.

The agent then stays connected, renewing its sign-in by itself. To
disconnect it, remove the connector in Claude or ChatGPT. Signing in only
works for people who already have a Soloclef account. If you lose access at
single sign-on, the agent stops working when its current sign-in runs out;
if your account is deactivated, at once.

## Coding agents and other clients

1. In Settings, API access, choose Create API key. Give it a name and, if
   you like, an expiry. Copy the key: it is shown only once.
2. Open your avatar menu, choose Connect your agent, and keep Remote
   selected.
3. Pick your agent's tab: Claude Code, Codex, OpenCode, Cursor, Claude
   Desktop, VS Code, Windsurf or Gemini CLI.
4. Follow its steps, pasting your key in place of `ss_pat_your_key_here`.

The key is sent as an `Authorization: Bearer` header. A client that can't
set that header can send it as `X-API-Key` instead.

In Claude Code, for example, run the command shown, then `/mcp` should list
soloclef as connected.

For Open WebUI, add a tool server of type MCP with your server's address
followed by `/mcp`, bearer authentication, and the key. To give it only the
academic searches, filter it to
`soloclef_pubmed_search,soloclef_arxiv_search,soloclef_preprint_search`.

## What the agent can do

Every tool's name starts with `soloclef_`:

- Search and read: `soloclef_search_knowledge_base`,
  `soloclef_list_documents`, `soloclef_get_document`.
- Change documents: `soloclef_add_document` adds a note from text or
  Markdown. `soloclef_update_document` replaces a document's text and keeps
  its title. `soloclef_delete_document` deletes for good.
  `soloclef_upload_file` uploads a file from the machine the MCP server runs
  on, so with Remote it cannot reach files on your computer; send their text
  with `soloclef_add_document` instead. New documents are indexed in the
  background.
- Academic search: `soloclef_pubmed_search`, `soloclef_arxiv_search` and
  `soloclef_preprint_search` for medRxiv and bioRxiv. Each returns up to 30
  results, 10 by default, and adds nothing to your documents.

The agent works on your documents, not your notebooks: it can't ask a
notebook's chat or make Studio outputs. Documents it adds can be picked into
a notebook with Add sources. It can't read your mail.

Limits:

- Each call waits up to 180 seconds.
- Results are cut to 20,000 characters, and each search passage to 500.
- `soloclef_search_knowledge_base` returns up to 20 results, 5 by default.

When a call is refused, the agent is told why, for example that API access is
off.

To cut an agent off, delete its key, or remove the connector in Claude or
ChatGPT. To cut off every agent at once, turn off API and agent access.
