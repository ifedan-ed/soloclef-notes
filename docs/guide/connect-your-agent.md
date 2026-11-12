# Connect your agent

Soloclef runs an MCP server at `/mcp` on this site. Claude (on the web, desktop or phone) and
ChatGPT connect by signing in with your single sign-on account; a coding agent
such as Claude Code, Codex or Cursor, or an MCP client such as Open WebUI,
connects with an API key. Either way the
agent can then search your documents, add to them, and run the academic
searches.

## Before you start

Turn on API access. In Settings → API access, switch on "API and agent
access". It is off for a new account, and until it is on every agent is
refused, whether it signed in or uses a key. While it is on, an agent can read
and change everything in your account.

## Claude and ChatGPT: sign in

Connecting lets you use your Soloclef account from inside Claude or
ChatGPT.

Open your avatar menu and choose Connect your agent. The top of the
dialog shows this server's address, a Client ID and a Client secret,
with copy buttons, and the steps for each:

- Claude: Customize → Connectors → Add custom connector (on a Team
  or Enterprise plan an Owner adds it under Organization settings →
  Connectors). Enter the server address, choose Use your own OAuth
  client (or open Advanced settings) and paste the client ID and
  secret. Then Connect and sign in. It works in Claude Desktop and the
  mobile app too.
- ChatGPT: turn on Developer mode under Settings → Apps &
  Connectors → Advanced settings, then create an app with the server
  address and OAuth, and paste the client ID and secret under Advanced
  OAuth settings (token method `client_secret_post`). Then sign in when
  asked.

You sign in on the single sign-on page and approve access. The approval page
appears every time you connect: approve only a connection you started
yourself, never one from a link someone sent you. The agent then
acts as your own Soloclef account, found by your email, and nothing
more. It stays connected through
automatic refreshes; to disconnect, remove the connector in Claude or
ChatGPT. Signing in only works for people who already have a Soloclef
account and may use Soloclef at single sign-on. Single sign-on stops
anyone else on its own page, and someone it lets in who has no account is
told no account matches. Everyone who may use Soloclef at single
sign-on can connect: no extra group or invitation is needed. The agent can't
create an account. If you don't have one yet, sign in to Soloclef on the web
first if the server lets people make their own accounts (single sign-on then
makes one); otherwise ask the administrator for one through the contact page
(`/contact` on your server).

## Coding agents: API key

Create an API key. In Settings → API access, choose Create
API key, give it a name and, if you like, an expiry. Copy the key when it
is shown: it is shown only once. Every account can make its own; a key acts
as you and nobody else.

Then open your avatar menu and choose Connect your agent.

1. Keep Remote selected: it uses this server, with nothing to install.
2. Pick your agent's tab: Claude Code, Codex, OpenCode, Cursor, Claude
   Desktop, VS Code, Windsurf or Gemini CLI.
3. Follow its steps and paste the configuration, replacing
   `ss_pat_your_key_here` with your key. The key travels as a
   `Authorization: Bearer` header. An agent that can't set that header can
   send the key as an `X-API-Key` header instead.

In Claude Code, for example, run the command shown, then `/mcp` inside Claude
Code should list soloclef as connected.

Open WebUI (and other MCP clients). Add a tool server of type MCP with
your server's address followed by `/mcp`, authentication bearer, and the
key. Open WebUI can be limited to some of the tools with its function-name
filter — to give it only the academic searches, list
`soloclef_pubmed_search,soloclef_arxiv_search,soloclef_preprint_search`.
Grant it to the accounts that should use it.

## What the agent can do

The tools are all named `soloclef_…`:

- Search and read documents: `soloclef_search_knowledge_base`,
  `soloclef_list_documents`, `soloclef_get_document`.
- Change documents: `soloclef_add_document` (a note in text or
  Markdown), `soloclef_upload_file`, `soloclef_update_document`,
  `soloclef_delete_document`. New documents are indexed in the background.
  `soloclef_upload_file` reads a file path on the machine where the MCP server
  runs. With Remote that is this server, so a file on your own computer
  can't be uploaded: send its text with `soloclef_add_document` instead, or
  use Self-host. Uploads use the Local parser, skip duplicates, and can be
  up to 500 MB. `soloclef_update_document` replaces a document's whole text
  and keeps its title; Studio outputs can't be changed. Deleting is permanent.
- Academic search: `soloclef_pubmed_search` (optionally limited to
  publication years, or with open-access full-text excerpts),
  `soloclef_arxiv_search` and `soloclef_preprint_search` (medRxiv or bioRxiv;
  preprints are not peer reviewed). Each returns up to 30 results (10 by
  default) for a query of up to 500 characters. They add nothing to your
  documents. There is no Google Scholar tool: Scholar offers no interface
  for programs to search it.

The agent works on your documents, not on notebooks: it can't ask a
notebook's chat or make Studio outputs. Documents it adds can be picked into a
notebook with Add sources. It can't read your email either: there are no
mail tools, and mail is never stored as documents.

Everything the agent does, it does as you: it can do with your documents
whatever you can.

### Limits

- An account allows 120 academic requests a minute, shared with research in
  its notebooks. Above that the agent is told it is rate limited.
- Each call waits up to 180 seconds.
- Results shown to the agent are cut to 20,000 characters, with each search
  passage cut to 500.
- `soloclef_search_knowledge_base` returns up to 20 results (5 by default).

When a call is refused, the agent passes on why: for example that API access
is off, or that it is rate limited.

To cut an agent off, delete its key, or for Claude and ChatGPT remove the
connector there. To cut off every agent at once, turn off "API and agent
access".

### Limits for signed-in agents

- A sign-in lasts an hour and the agent refreshes it on its own, for up to 30
  days without signing in again.
- If your access to Soloclef at single sign-on is taken away, the agent
  stops working within an hour; if your Soloclef account is
  deactivated, at once.
- The server remembers that a sign-in is valid for up to a minute, then asks
  again.
- Agents that can't paste a client ID and secret (Claude Code and other
  coding agents) use an API key instead.
