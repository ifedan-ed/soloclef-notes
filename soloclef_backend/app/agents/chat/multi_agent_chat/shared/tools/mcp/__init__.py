"""MCP (Model Context Protocol) integration: client, tool loading, and cache.

Split by responsibility:
- ``client``: the connection test for remote servers (stdio is not supported).
- ``tool``: discovery + LangChain tool construction and cache invalidation.
- ``cache``: the connector tool-cache refresh helpers.
"""
