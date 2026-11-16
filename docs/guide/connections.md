# Connectors

Connectors link your account to other services: your files in Google Drive
and Nextcloud, your mailboxes, web search with Exa or Tavily, and MCP
servers. They are under Settings, Connectors. Each connection is yours alone,
and is used by all your notebooks, chats and automations.

The page shows a card for each kind. Your connectors, on the left, lists the
ones you have; pick one to open its settings. Some cards depend on the
server: Nextcloud only appears when the server has one, and Google Drive says
when it isn't set up.

Google Drive, Nextcloud and Email (IMAP) connections have an Enabled switch.
Switched off, a connection stays connected but nothing uses it, and it is
marked Off. Disconnect, in a connection's settings, removes it. Files you
already added from it stay in your library.

Every password, key and token you give a connector is stored encrypted and is
never shown again.

## Google Drive

1. Choose Connect on the Google Drive card. You can also connect from a
   notebook, under Add sources, From a connector.
2. Approve access on Google's page, in the same browser, within 10 minutes.

Soloclef can only see the files you pick in Google's picker and the files it
creates for you. The rest of your Drive stays out of reach. Pick files from a
notebook, as described in
[Notebooks and sources](notebooks-and-sources.md#google-drive-and-nextcloud).

Picked files are checked for changes once a day. You can change how often in
the connection's settings, from every few minutes to weekly. A file you delete
in Drive stays in your library and is no longer checked.

When you ask, the chat can also create a file in your Drive, or move one of
these files to the bin.

Disconnect removes Soloclef's access at Google. If Google doesn't confirm it,
the page says so; then remove the app under Security, Third-party
connections, in your Google Account.

## Nextcloud

Choose Connect on the Nextcloud card and sign in to Nextcloud in the tab that
opens. The page connects by itself once you grant access. Then pick files
from a notebook, as described in
[Notebooks and sources](notebooks-and-sources.md#google-drive-and-nextcloud).

Files from Nextcloud are copied once and don't stay in sync. When you ask,
the chat can also create files in your Nextcloud or move them to its bin.

## Email

Email (IMAP) and Stalwart (JMAP) add mailboxes for the assistant to read. See
[Email](email.md).

## Exa and Tavily

Exa and Tavily are web search services you use with your own API key.

1. Open the Exa or Tavily card. Get a key from Exa, or from Tavily, links to
   the provider's key page.
2. Paste the key and choose Connect. The key is checked with the provider
   before it is saved.

The card then shows only the key's last characters. Replace key swaps it, and
Disconnect deletes it.

Once connected, the research box in a notebook can search with it, and so can
the chat's Tools menu and your automations. Searches run on your key, so
their cost is on your account with that service.

## MCP servers

An MCP server gives the chat and your automations more tools: another app's
search, records or actions. Choose Add MCP server on the MCPs card and fill
in:

- Name and Server URL.
- Transport: Streamable HTTP, which most servers use, or SSE, only if the
  server's instructions say so.
- Authentication:
    - None.
    - API key in a header: the header's name and the key.
    - Bearer token.
    - OAuth sign-in. After you enter the URL, the form checks how the server
      signs in. If it lets apps register themselves, nothing more is needed.
      If not, enter the Client ID you registered there, and the Client secret
      if there is one. OAuth is only offered when the server you use
      Soloclef on has it set up.
- Extra headers, if the server needs more than the authentication header, up
  to 20.

Test connection connects and lists the server's tools before you save. An
OAuth server is tested after you save it, once you have signed in on its own
page. Its settings later offer Sign in again. Removing the server also ends
that sign-in at the server.

A saved key or token shows as "Saved, ends in" its last four characters,
with Replace to change it. Saving without replacing it keeps the old one.

The server's address must be https on the standard port, with a public
domain name, not an IP address or a name that only works inside a network.
Local MCP servers that run as a program on your computer are not supported.

The chat uses a server's tools when you ask about what it holds, and cites
each result with a chip naming the app and the tool. In the chat's + menu,
MCP connectors lists your servers, and Manage tools lets you switch single
tools off. Every tool runs without asking unless you mark it Ask first under
Settings, Agent permissions (see
[Chat and citations](chat-and-citations.md#ask-first)).

A server an administrator adds must be on the site's list of allowed hosts.
