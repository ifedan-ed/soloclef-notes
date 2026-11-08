"""A specialist stops re-sending every old page on each model call."""

from __future__ import annotations

from dataclasses import dataclass, replace
from types import SimpleNamespace
from typing import Any

import pytest
from langchain_core.messages import AIMessage, HumanMessage, ToolMessage

from app.agents.chat.multi_agent_chat.subagents.shared.context_budget import (
    SUBAGENT_KEEP_TOOL_RESULTS,
    build_subagent_context_budget_mw,
)

pytestmark = pytest.mark.unit

_FLAGS = SimpleNamespace(enable_context_editing=True, disable_new_agent_stack=False)


def _tools(*names: str) -> list[Any]:
    return [SimpleNamespace(name=name) for name in names]


@dataclass
class _Request:
    messages: list[Any]

    def override(self, **changes: Any) -> _Request:
        return replace(self, **changes)


def _crawl_history(pages: int) -> list[Any]:
    messages: list[Any] = [HumanMessage(content="find the national average")]
    for i in range(pages):
        messages.append(
            AIMessage(
                content="",
                tool_calls=[{"name": "web_crawl", "args": {"i": i}, "id": f"c{i}"}],
            )
        )
        messages.append(
            ToolMessage(
                content=f"page {i} " + "x" * 40_000,
                tool_call_id=f"c{i}",
                name="web_crawl",
                id=f"t{i}",
            )
        )
    return messages


async def test_older_pages_go_out_as_a_short_reference_the_newest_stay_whole():
    middleware = build_subagent_context_budget_mw(
        flags=_FLAGS,
        tools=_tools("web_crawl", "read_run", "search_run"),
        workspace_id=1,
        budget_tokens=20_000,
    )
    assert middleware is not None
    spilled: list[Any] = []

    async def _flush(pending):
        spilled.extend(pending)

    middleware._flush_spills = _flush  # no database in a unit test
    history = _crawl_history(8)
    sent: list[Any] = []

    async def handler(request):
        sent.extend(request.messages)
        return "model reply"

    assert (
        await middleware.awrap_model_call(_Request(history), handler) == "model reply"
    )

    tool_results = [m for m in sent if isinstance(m, ToolMessage)]
    whole = [m for m in tool_results if m.content.startswith("page ")]
    cleared = [m for m in tool_results if "spill_" in m.content]
    assert (
        whole[-SUBAGENT_KEEP_TOOL_RESULTS:]
        == tool_results[-SUBAGENT_KEEP_TOOL_RESULTS:]
    )
    assert cleared and all("read_run" in m.content for m in cleared)
    assert len(spilled) == len(cleared)
    # The agent's saved conversation is untouched; only the model's view shrank.
    assert all(
        m.content.startswith("page ") for m in history if isinstance(m, ToolMessage)
    )


def test_only_specialists_that_can_read_back_get_a_budget():
    assert (
        build_subagent_context_budget_mw(
            flags=_FLAGS, tools=_tools("search_emails", "read_email"), workspace_id=1
        )
        is None
    )
    assert (
        build_subagent_context_budget_mw(
            flags=_FLAGS,
            tools=_tools("web_crawl", "read_run"),
            workspace_id=1,
            budget_tokens=0,
        )
        is None
    )
    off = SimpleNamespace(enable_context_editing=False, disable_new_agent_stack=False)
    assert (
        build_subagent_context_budget_mw(
            flags=off, tools=_tools("web_crawl", "read_run"), workspace_id=1
        )
        is None
    )
