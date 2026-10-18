"""A notebook chat's assembled agent retrieves from the sources only.

Drives the production multi-agent graph (real DB, scripted LLM) the way a
notebook turn builds it: whatever scope the client sent, no web specialist
exists, so a ``task(google_search, ...)`` the model tries anyway cannot run,
and the system prompt tells it the web is not part of answering. A tool the
user picks from the composer's Tools menu adds exactly that specialist for the
message. A plain chat asking for the web keeps it.
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
from app.agents.chat.retrieval_scope import RetrievalScope, SourcesOnly
from app.agents.chat.tool_preference import ToolPreference
from app.notebooks.chat.pins import NotebookPins, notebook_retrieval_scope
from app.services.connector_service import ConnectorService
from tests.integration.harness import ScriptedTurn, build_scripted_harness

pytestmark = pytest.mark.integration

PINS = NotebookPins(notebook_id=1, title="n", document_ids=[7], scope_document_ids=[7])


class _Prompts(BaseCallbackHandler):
    """Records the system prompt of every model call."""

    def __init__(self) -> None:
        self.system: list[str] = []

    def on_chat_model_start(self, serialized, messages, **kwargs: Any) -> None:
        for batch in messages:
            for message in batch:
                if message.type == "system":
                    self.system.append(message.text)


def _task(subagent: str, call_id: str) -> ScriptedTurn:
    return ScriptedTurn(
        tool_calls=[
            {
                "name": "task",
                "args": {"subagent_type": subagent, "description": "find news"},
                "id": call_id,
            }
        ]
    )


async def _run(
    db_session,
    db_user,
    db_workspace,
    *,
    subagent: str,
    scope: RetrievalScope,
    sources_only: SourcesOnly | None,
    thread: str,
):
    harness = build_scripted_harness(
        turns=[
            _task(subagent, f"call_{thread}"),
            # Consumed by the subagent when it exists, else by the main agent.
            ScriptedTurn(text="subagent result"),
            ScriptedTurn(text="final answer"),
        ]
    )
    # A compiled graph is cached with the model it was built with; each run
    # scripts its own model, so it must build its own graph.
    get_cache().clear()
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
    task_messages = [
        m for m in result["messages"] if isinstance(m, ToolMessage) and m.name == "task"
    ]
    assert len(task_messages) == 1
    return str(task_messages[0].content), prompts.system[0]


def _delegated(task_output: str) -> bool:
    return "subagent result" in task_output


@pytest.mark.asyncio
@pytest.mark.parametrize("requested", list(RetrievalScope))
async def test_a_notebook_turn_has_no_web_specialist_whatever_it_asked_for(
    db_session, db_user, db_workspace, requested
):
    scope, sources_only = notebook_retrieval_scope(requested, PINS)

    output, system = await _run(
        db_session,
        db_user,
        db_workspace,
        subagent="google_search",
        scope=scope,
        sources_only=sources_only,
        thread=f"nb-{requested.value}",
    )

    assert not _delegated(output), output
    assert "This is a notebook chat" in system
    assert "Do not search the web or offer to" in system


@pytest.mark.asyncio
async def test_a_tools_menu_pick_adds_only_that_specialist(
    db_session, db_user, db_workspace
):
    scope, sources_only = notebook_retrieval_scope(
        RetrievalScope.ALL, PINS, preferred_tool=ToolPreference.GOOGLE_SEARCH
    )

    picked, system = await _run(
        db_session,
        db_user,
        db_workspace,
        subagent="google_search",
        scope=scope,
        sources_only=sources_only,
        thread="nb-pick",
    )
    other, _ = await _run(
        db_session,
        db_user,
        db_workspace,
        subagent="reddit",
        scope=scope,
        sources_only=sources_only,
        thread="nb-pick-other",
    )

    assert _delegated(picked), picked
    assert not _delegated(other), other
    assert "picked the google_search tool" in system
    assert "comes from the web, not from the notebook's sources" in system


@pytest.mark.asyncio
async def test_a_plain_chat_asking_for_the_web_keeps_it(
    db_session, db_user, db_workspace
):
    scope, sources_only = notebook_retrieval_scope(RetrievalScope.WEB, None)

    output, system = await _run(
        db_session,
        db_user,
        db_workspace,
        subagent="google_search",
        scope=scope,
        sources_only=sources_only,
        thread="plain-web",
    )

    assert _delegated(output), output
    assert "notebook chat" not in system
