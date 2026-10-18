"""Exa, Tavily and Browserbase in chat: built only where the workspace has the
key, as web specialists (never connected-app reads), each with its one tool."""

from __future__ import annotations

import pytest

from app.agents.chat.keyed_web_specialists import (
    KEYED_WEB_SPECIALISTS,
    connected_keyed_web_specialists,
    excluded_keyed_web_specialists,
)
from app.agents.chat.multi_agent_chat.shared.feature_flags import AgentFeatureFlags
from app.agents.chat.multi_agent_chat.subagents.registry import (
    SUBAGENT_BUILDERS_BY_NAME,
    get_subagents_to_exclude,
    main_prompt_registry_subagent_lines,
)
from app.agents.chat.retrieval_scope import WEB_RETRIEVAL_SUBAGENTS
from app.db import SearchSourceConnectorType

pytestmark = pytest.mark.unit

MCP = SearchSourceConnectorType.MCP_CONNECTOR


def _mcp(url: str, headers: dict | None = None) -> tuple:
    return (MCP, {"server_config": {"url": url, "headers": headers or {}}})


class _Rows:
    def __init__(self, rows):
        self._rows = rows

    def all(self):
        return self._rows


class _Session:
    def __init__(self, rows):
        self.rows = rows

    async def execute(self, _statement):
        return _Rows(self.rows)


async def test_only_specialists_with_a_key_are_connected():
    session = _Session(
        [
            _mcp("https://mcp.exa.ai/mcp", {"x-api-key": "exa-key"}),
            _mcp("https://mcp.browserbase.com/mcp", {"x-bb-api-key": "bb-key"}),
            # Tavily's server added without its key, and an unrelated server.
            _mcp("https://mcp.tavily.com/mcp"),
            _mcp("https://cloud.example.org/mcp", {"authorization": "Bearer x"}),
        ]
    )

    connected = await connected_keyed_web_specialists(session, 1)

    assert connected == {"exa_search", "browserbase"}
    assert excluded_keyed_web_specialists(connected) == {"tavily_search"}


async def test_a_workspace_without_keys_gets_none():
    assert await connected_keyed_web_specialists(_Session([]), 1) == frozenset()
    assert excluded_keyed_web_specialists(None) == set(KEYED_WEB_SPECIALISTS)


def test_each_keyed_specialist_is_a_registered_web_specialist():
    for name in KEYED_WEB_SPECIALISTS:
        assert name in SUBAGENT_BUILDERS_BY_NAME
        assert name in WEB_RETRIEVAL_SUBAGENTS


def test_they_need_an_mcp_connector_where_the_keys_live():
    without = set(get_subagents_to_exclude([]))
    with_mcp = set(get_subagents_to_exclude(["MCP_CONNECTOR"]))

    assert set(KEYED_WEB_SPECIALISTS) <= without
    assert not (set(KEYED_WEB_SPECIALISTS) & with_mcp)


def test_the_router_reads_what_each_one_is_for():
    lines = dict(main_prompt_registry_subagent_lines([]))

    assert "Exa" in lines["exa_search"]
    assert "Tavily" in lines["tavily_search"]
    assert "real cloud browser" in lines["browserbase"]
    assert "sole path" not in lines["google_search"]


@pytest.mark.parametrize(
    ("name", "tool"),
    [
        ("exa_search", "exa_web_search"),
        ("tavily_search", "tavily_web_search"),
        ("browserbase", "browserbase_read_page"),
    ],
)
def test_each_specialist_carries_its_one_tool_and_the_run_readers(name, tool):
    spec = SUBAGENT_BUILDERS_BY_NAME[name](
        dependencies={"workspace_id": 7, "flags": AgentFeatureFlags()}
    ).spec

    names = {t.name for t in spec["tools"]}
    assert tool in names
    assert {"read_run", "search_run"} <= names
    assert spec["name"] == name
