"""``browserbase`` sub-agent tools: read a page in Browserbase on the workspace's own key."""

from __future__ import annotations

from typing import Any

from langchain_core.tools import BaseTool

from app.agents.chat.multi_agent_chat.shared.permissions import Ruleset
from app.capabilities.browserbase.read_page.definition import read_page_capability
from app.capabilities.core.access.agent import build_capability_tools

NAME = "browserbase"

RULESET = Ruleset(origin=NAME, rules=[])


def load_tools(
    *, dependencies: dict[str, Any] | None = None, **kwargs: Any
) -> list[BaseTool]:
    d = {**(dependencies or {}), **kwargs}
    workspace_id = d.get("workspace_id")
    return build_capability_tools(
        workspace_id=workspace_id,
        capabilities=[read_page_capability(workspace_id)],
    )
