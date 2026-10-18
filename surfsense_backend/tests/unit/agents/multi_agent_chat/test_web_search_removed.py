"""Guardrail: the multi-engine ``web_search`` tool is fully retired.

Public web search runs exclusively through the ``google_search`` subagent, and
the classic search-API connector types are gone. This pins those invariants so
a future change can't quietly bring the ``web_search`` tool or a search
connector type back.
"""

from __future__ import annotations

import pytest

from app.agents.chat.multi_agent_chat.main_agent.tools.index import (
    MAIN_AGENT_SURFSENSE_TOOL_NAMES,
)
from app.agents.chat.multi_agent_chat.subagents.registry import (
    SUBAGENT_BUILDERS_BY_NAME,
)
from app.db import SearchSourceConnectorType

pytestmark = pytest.mark.unit

_REMOVED_SEARCH_TYPES = (
    "SERPER_API",
    "TAVILY_API",
    "SEARXNG_API",
    "LINKUP_API",
    "BAIDU_SEARCH_API",
)


def test_web_search_tool_removed_from_main_agent():
    assert "web_search" not in MAIN_AGENT_SURFSENSE_TOOL_NAMES


def test_google_search_specialist_present():
    assert "google_search" in SUBAGENT_BUILDERS_BY_NAME


@pytest.mark.parametrize("connector_type", _REMOVED_SEARCH_TYPES)
def test_search_connector_types_removed(connector_type):
    assert connector_type not in {t.value for t in SearchSourceConnectorType}
