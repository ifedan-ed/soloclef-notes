"""``tavily_search`` sub-agent tools: Tavily web search on the workspace's own key."""

from __future__ import annotations

from typing import Any

from langchain_core.tools import BaseTool

from app.agents.chat.multi_agent_chat.shared.permissions import Ruleset
from app.capabilities.core.access.agent import build_capability_tools
from app.capabilities.web_search_api.definitions import web_search_capability

NAME = "tavily_search"

RULESET = Ruleset(origin=NAME, rules=[])


def load_tools(
    *, dependencies: dict[str, Any] | None = None, **kwargs: Any
) -> list[BaseTool]:
    d = {**(dependencies or {}), **kwargs}
    workspace_id = d.get("workspace_id")
    return build_capability_tools(
        workspace_id=workspace_id,
        capabilities=[web_search_capability("tavily", workspace_id)],
    )
