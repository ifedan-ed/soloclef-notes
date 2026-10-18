"""Regression test for the ``search_knowledge_base`` tool-switch bypass.

Disabling ``search_knowledge_base`` in the composer's "Manage Tools" sheet
only ever filtered that tool out of the ``knowledge_base`` write subagent's
own tool list. The ``ask_knowledge_base`` tool is injected into *every other*
subagent (``build_subagents``/``_inject_ask_kb_tool_in_place`` in
``subagents/registry.py``) and, when called, lazily compiles a fresh
read-only ``knowledge_base`` subagent carrying its own unfiltered
``search_knowledge_base`` tool — so a user who disabled knowledge-base search
could still reach it from the amazon/youtube/web_crawler/etc. subagents.
``_search_knowledge_base_disabled`` is the gate that stops
``build_main_agent_deepagent_middleware`` from building ``ask_knowledge_base``
at all once the matching catalog entry is disabled.
"""

from __future__ import annotations

import pytest

from app.agents.chat.multi_agent_chat.main_agent.middleware.stack import (
    _search_knowledge_base_disabled,
)

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    "disabled_tools",
    [
        ["search_knowledge_base"],
        ["generate_podcast", "search_knowledge_base"],
        ["search_knowledge_base", "search_knowledge_base"],
    ],
)
def test_disabled_when_catalog_name_is_present(disabled_tools: list[str]) -> None:
    assert _search_knowledge_base_disabled(disabled_tools) is True


@pytest.mark.parametrize(
    "disabled_tools",
    [None, [], ["generate_podcast"], ["ask_knowledge_base"]],
)
def test_enabled_when_catalog_name_is_absent(disabled_tools: list[str] | None) -> None:
    # Note: "ask_knowledge_base" (the alias tool's own name) is NOT the
    # catalog name a user can pick in the UI — only "search_knowledge_base"
    # is listed in TOOL_CATALOG, so disabling by that alias name must not be
    # mistaken for the real switch.
    assert _search_knowledge_base_disabled(disabled_tools) is False
