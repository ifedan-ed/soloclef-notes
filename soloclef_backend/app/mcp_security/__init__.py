"""Guards for user-configured MCP servers.

Only remote (HTTPS) MCP servers can be added, tested or called. A workspace
Owner or Editor may add their own, on any public host, with their own key; a
server an administrator adds is a site server and its host must be on the
admin-maintained allowlist (``server_origin``). Every connection resolves the
host itself, refuses non-public addresses, pins the checked address and never
follows redirects. Local (stdio) servers are not supported at all: they ran a
command inside the backend container. Stored credentials are write-only
through the API.
"""
