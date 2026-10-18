"""Connector-type to subagent name; subagent name to availability tokens for build_subagents."""

from __future__ import annotations

# User-added MCP servers route to the ``mcp_discovery`` subagent. Google Drive
# stays native (it enriches the knowledge base).
CONNECTOR_TYPE_TO_CONNECTOR_AGENT_MAPS: dict[str, str] = {
    "MCP_CONNECTOR": "mcp_discovery",
    "GOOGLE_DRIVE_CONNECTOR": "google_drive",
    "EMAIL_IMAP_CONNECTOR": "email_imap",
}

# ``mcp_discovery`` is present iff the workspace has at least one MCP server.
SUBAGENT_TO_REQUIRED_CONNECTOR_MAP: dict[str, frozenset[str]] = {
    "amazon": frozenset(),
    "deliverables": frozenset(),
    "knowledge_base": frozenset(),
    "web_crawler": frozenset(),
    "youtube": frozenset(),
    "google_maps": frozenset(),
    "google_search": frozenset(),
    "indeed": frozenset(),
    "reddit": frozenset(),
    "instagram": frozenset(),
    "tiktok": frozenset(),
    "walmart": frozenset(),
    "mcp_discovery": frozenset({"MCP_CONNECTOR"}),
    # Their keys live in MCP connectors; ``keyed_web_specialists`` then keeps
    # only the ones whose provider's key is actually there.
    "exa_search": frozenset({"MCP_CONNECTOR"}),
    "tavily_search": frozenset({"MCP_CONNECTOR"}),
    "browserbase": frozenset({"MCP_CONNECTOR"}),
    "google_drive": frozenset({"GOOGLE_DRIVE_FILE"}),
    "email_imap": frozenset({"EMAIL_IMAP_CONNECTOR"}),
}
