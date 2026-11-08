"""Routing guardrails for the ``mcp_discovery`` subagent.

User-added MCP servers route to ``mcp_discovery``; Google Drive keeps its own
route. These tests pin the connector→route mapping, the gating, tool-name
collision prefixing, and the metadata-derived approval ruleset.
"""

from __future__ import annotations

import pytest
from langchain_core.tools import StructuredTool

from app.agents.chat.multi_agent_chat.constants import (
    CONNECTOR_TYPE_TO_CONNECTOR_AGENT_MAPS,
    SUBAGENT_TO_REQUIRED_CONNECTOR_MAP,
)
from app.agents.chat.multi_agent_chat.subagents.builtins.mcp_discovery.tools.index import (
    NAME as MCP_DISCOVERY_NAME,
    build_ruleset,
)
from app.agents.chat.multi_agent_chat.subagents.mcp_tools.index import (
    resolve_tool_name_collisions,
)
from app.agents.chat.multi_agent_chat.subagents.registry import (
    get_subagents_to_exclude,
)

pytestmark = pytest.mark.unit


def _tool(name: str, metadata: dict) -> StructuredTool:
    return StructuredTool.from_function(
        func=lambda: name,
        name=name,
        description=name,
        metadata=metadata,
    )


def test_connector_routes():
    assert CONNECTOR_TYPE_TO_CONNECTOR_AGENT_MAPS == {
        "MCP_CONNECTOR": MCP_DISCOVERY_NAME,
        "GOOGLE_DRIVE_CONNECTOR": "google_drive",
        "EMAIL_IMAP_CONNECTOR": "email_imap",
    }


def test_discovery_needs_an_mcp_server():
    assert MCP_DISCOVERY_NAME not in get_subagents_to_exclude(["MCP_CONNECTOR"])
    assert MCP_DISCOVERY_NAME in get_subagents_to_exclude([])
    assert SUBAGENT_TO_REQUIRED_CONNECTOR_MAP[MCP_DISCOVERY_NAME] == frozenset(
        {"MCP_CONNECTOR"}
    )


def test_collision_only_prefixes_shared_names():
    """A name on two connectors is prefixed; a unique name is left untouched."""
    tools = [
        _tool("search", {"mcp_connector_id": 1, "mcp_transport": "http"}),
        _tool("search", {"mcp_connector_id": 2, "mcp_transport": "http"}),
        _tool("list_bases", {"mcp_connector_id": 2, "mcp_transport": "http"}),
    ]
    resolved = {t.name: t for t in resolve_tool_name_collisions(tools)}

    assert "list_bases" in resolved
    assert "search" not in resolved
    assert "mcp_1_search" in resolved
    assert "mcp_2_search" in resolved
    for name in ("mcp_1_search", "mcp_2_search"):
        meta = resolved[name].metadata or {}
        assert meta["mcp_original_tool_name"] == "search"
        assert meta["mcp_collision_prefixed"] is True


def test_collision_noop_without_collisions():
    tools = [
        _tool("a", {"mcp_connector_id": 1}),
        _tool("b", {"mcp_connector_id": 2}),
    ]
    assert [t.name for t in resolve_tool_name_collisions(tools)] == ["a", "b"]


def test_ruleset_reads_hitl_from_metadata():
    """Read-only MCP tools ``allow``; every other MCP tool ``ask``; natives skip."""
    tools = [
        _tool("readonly_search", {"mcp_transport": "http", "hitl": False}),
        _tool("mutating_create", {"mcp_transport": "http", "hitl": True}),
        _tool("native_helper", {}),  # no mcp_transport => no rule
    ]
    rules = {r.permission: r.action for r in build_ruleset(tools).rules}
    assert rules == {"readonly_search": "allow", "mutating_create": "ask"}
