"""Exa, Tavily and Browserbase in a notebook chat, on the production graph.

Drives the real multi-agent graph (real DB, scripted LLM) the way a notebook
turn builds it. Picking Exa from the composer's Tools menu adds the Exa
specialist when the workspace has an Exa key in one of its MCP connectors,
and nothing when it has none; without a pick the turn stays sources-only.
"""

from __future__ import annotations

from typing import Any

import pytest
from langchain_core.callbacks import BaseCallbackHandler
from langchain_core.messages import HumanMessage, ToolMessage
from langgraph.checkpoint.memory import InMemorySaver

from app.agents.chat.multi_agent_chat import create_multi_agent_chat_deep_agent
from app.agents.chat.multi_agent_chat.main_agent.runtime.agent_cache_store import (
    get_cache,
)
from app.agents.chat.multi_agent_chat.shared.tools.mcp.tool import (
    invalidate_mcp_tools_cache,
)
from app.agents.chat.retrieval_scope import RetrievalScope
from app.agents.chat.tool_preference import ToolPreference
from app.db import SearchSourceConnector, SearchSourceConnectorType
from app.notebooks.chat.pins import NotebookPins, notebook_retrieval_scope
from app.services.connector_service import (
    ConnectorService,
    invalidate_connector_discovery_cache,
)
from tests.integration.harness import ScriptedTurn, build_scripted_harness

pytestmark = pytest.mark.integration

PINS = NotebookPins(notebook_id=1, title="n", document_ids=[7], scope_document_ids=[7])


class _Prompts(BaseCallbackHandler):
    def __init__(self) -> None:
        self.system: list[str] = []

    def on_chat_model_start(self, serialized, messages, **kwargs: Any) -> None:
        for batch in messages:
            for message in batch:
                if message.type == "system":
                    self.system.append(message.text)


async def _exa_connector(db_session, db_user, db_workspace) -> None:
    db_session.add(
        SearchSourceConnector(
            name="Exa web search",
            connector_type=SearchSourceConnectorType.MCP_CONNECTOR,
            is_indexable=False,
            config={
                "server_config": {
                    "url": "https://mcp.exa.ai/mcp",
                    "transport": "streamable-http",
                    "headers": {"x-api-key": "exa-test-key"},
                },
                # Listed tools, so building the turn never calls the server.
                "cached_tools": {
                    "discovered_at": "2026-10-09T00:00:00Z",
                    "tools": [
                        {
                            "name": "web_search_exa",
                            "description": "Search the web.",
                            "input_schema": {
                                "type": "object",
                                "properties": {"query": {"type": "string"}},
                            },
                        }
                    ],
                },
            },
            workspace_id=db_workspace.id,
            user_id=db_user.id,
        )
    )
    await db_session.flush()


async def _run(db_session, db_user, db_workspace, *, subagent, preferred, thread):
    harness = build_scripted_harness(
        turns=[
            ScriptedTurn(
                tool_calls=[
                    {
                        "name": "task",
                        "args": {"subagent_type": subagent, "description": "search"},
                        "id": f"call_{thread}",
                    }
                ]
            ),
            # Consumed by the subagent when it exists, else by the main agent.
            ScriptedTurn(text="subagent result"),
            ScriptedTurn(text="final answer"),
        ]
    )
    get_cache().clear()
    invalidate_connector_discovery_cache(db_workspace.id)
    invalidate_mcp_tools_cache(db_workspace.id)
    scope, sources_only = notebook_retrieval_scope(
        RetrievalScope.ALL, PINS, preferred_tool=preferred
    )
    agent = await create_multi_agent_chat_deep_agent(
        llm=harness.model,
        workspace_id=db_workspace.id,
        db_session=db_session,
        connector_service=ConnectorService(db_session),
        checkpointer=InMemorySaver(),
        user_id=str(db_user.id),
        thread_id=db_workspace.id,
        agent_config=None,
        retrieval_scope=scope,
        sources_only=sources_only,
    )
    prompts = _Prompts()
    result = await agent.ainvoke(
        {"messages": [HumanMessage(content="what is new on the web?")]},
        config={"configurable": {"thread_id": thread}, "callbacks": [prompts]},
    )
    [task] = [
        m for m in result["messages"] if isinstance(m, ToolMessage) and m.name == "task"
    ]
    return str(task.content), prompts.system[0]


@pytest.mark.asyncio
async def test_picking_exa_adds_the_exa_specialist_where_the_key_exists(
    db_session, db_user, db_workspace
):
    await _exa_connector(db_session, db_user, db_workspace)

    picked, system = await _run(
        db_session,
        db_user,
        db_workspace,
        subagent="exa_search",
        preferred=ToolPreference.EXA,
        thread="exa-picked",
    )
    unpicked, _ = await _run(
        db_session,
        db_user,
        db_workspace,
        subagent="exa_search",
        preferred=None,
        thread="exa-unpicked",
    )
    # Tavily was picked, but this workspace has no Tavily key.
    no_key, no_key_system = await _run(
        db_session,
        db_user,
        db_workspace,
        subagent="tavily_search",
        preferred=ToolPreference.TAVILY,
        thread="tavily-no-key",
    )

    assert "subagent result" in picked, picked
    assert "picked the exa_search tool" in system
    assert "Connected-app data is not retrieved either" in system
    assert "subagent result" not in unpicked, unpicked
    assert "subagent result" not in no_key, no_key
    # ...and the prompt does not offer it: it says it is not available.
    assert "task(tavily_search" not in no_key_system
    assert "picked the tavily_search tool in the composer, but" in no_key_system
    assert "not available and must not be called" in no_key_system


@pytest.mark.asyncio
async def test_without_any_key_picking_exa_adds_nothing(
    db_session, db_user, db_workspace
):
    output, _ = await _run(
        db_session,
        db_user,
        db_workspace,
        subagent="exa_search",
        preferred=ToolPreference.EXA,
        thread="exa-nothing",
    )

    assert "subagent result" not in output, output
