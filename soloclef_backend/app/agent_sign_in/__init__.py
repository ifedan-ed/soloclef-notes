"""AI agents (claude.ai, ChatGPT) signing in to Notes through Authentik.

The MCP server at ``/mcp`` used to accept only a Notes API key in a header,
which claude.ai's and ChatGPT's custom connectors cannot send. They speak the
MCP authorization spec instead: the person signs in at the provider and the
agent sends the Authentik access token it got. This slice checks such a token
(signature from the provider's JWKS, issuer, audience = the agent client,
expiry) and finds the Notes account it belongs to, read-only, by the same
rule as the web's single sign-on button: the identity's linked subject first, else
the account with the same verified email. It never creates an account.
Everyone Authentik lets into Notes may connect; each token says whether
Authentik still does (see ``access_token.NOTES_ACCESS_CLAIM``).

An agent signed in this way is gated exactly like an API key: it sees only the
workspaces whose "API key access" is on, and session-only actions refuse it.
Mounted only when ``MCP_OAUTH_ISSUER`` and ``MCP_OAUTH_CLIENT_ID`` are set; see
"AI agents sign in with Authentik" in ``custom/README.md``.
"""
