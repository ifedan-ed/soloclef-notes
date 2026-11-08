"""``mcp_discovery`` tools + metadata-derived permission ruleset.

Assembles the toolset from two sources:

1. **``get_connected_accounts``** — read-only discovery helper.
2. **MCP tools** — injected at runtime by the factory (already
   collision-resolved). Loaded with ``bypass_internal_hitl=True``, so their
   *only* gate is this subagent's :class:`PermissionMiddleware`; the ruleset
   is derived from each tool's ``metadata['hitl']`` flag.
"""

from __future__ import annotations

from typing import Any

from langchain_core.tools import BaseTool

from app.agents.chat.multi_agent_chat.shared.permissions import Rule, Ruleset

from .get_connected_accounts import create_get_connected_accounts_tool

NAME = "mcp_discovery"


def load_tools(
    *,
    dependencies: dict[str, Any] | None = None,
    mcp_tools: list[BaseTool] | None = None,
    **kwargs: Any,
) -> list[BaseTool]:
    """``get_connected_accounts`` + injected MCP tools."""
    d = {**(dependencies or {}), **kwargs}
    return [
        create_get_connected_accounts_tool(workspace_id=d["workspace_id"]),
        *(mcp_tools or []),
    ]


def _is_mcp_tool(tool: BaseTool) -> bool:
    meta = getattr(tool, "metadata", None) or {}
    return "mcp_transport" in meta


def build_ruleset(tools: list[BaseTool]) -> Ruleset:
    """Derive the approval ruleset from tool metadata.

    Only MCP tools get rules: read-only ones (``metadata['hitl'] is False``)
    are ``allow``, every other MCP tool is ``ask``. ``get_connected_accounts``
    carries no rule and falls through to the ``allow */*`` default (it is
    read-only).
    """
    rules: list[Rule] = []
    for tool in tools:
        if not _is_mcp_tool(tool):
            continue
        meta = getattr(tool, "metadata", None) or {}
        action = "allow" if meta.get("hitl") is False else "ask"
        rules.append(Rule(permission=tool.name, pattern="*", action=action))
    return Ruleset(origin=NAME, rules=rules)
