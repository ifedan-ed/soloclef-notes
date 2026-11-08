"""``POST /threads/{id}/regenerate`` re-asks the saved question from before it.

The turn itself (``stream_new_chat``) is replaced by a fake that records what
the route asked for and, like a real turn, writes the re-run's checkpoints
from the rewind point it was given. Everything else is the real route on the
test database and the real Postgres checkpointer.
"""

from __future__ import annotations

from collections.abc import AsyncGenerator
from typing import Annotated, Any

import httpx
import pytest
import pytest_asyncio
from httpx import ASGITransport
from langchain_core.messages import AIMessage, AnyMessage, HumanMessage
from langgraph.graph import END, START, StateGraph
from langgraph.graph.message import add_messages
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from typing_extensions import TypedDict

from app.agents.chat.retrieval_scope import RetrievalScope
from app.agents.chat.tool_preference import ToolPreference
from app.app import app, limiter
from app.auth.context import AuthContext
from app.db import NewChatMessage, User, get_async_session
from app.routes import new_chat_routes
from app.tasks.chat.streaming.flows.regenerate import turn_options_configurable
from app.users import get_auth_context

pytestmark = pytest.mark.integration

limiter.enabled = False


class _State(TypedDict):
    messages: Annotated[list[AnyMessage], add_messages]


def _graph(saver, answer: str):
    graph = StateGraph(_State)
    graph.add_node("model", lambda _s: {"messages": [AIMessage(answer)]})
    graph.add_edge(START, "model")
    graph.add_edge("model", END)
    return graph.compile(checkpointer=saver)


async def _run_turn(
    saver,
    thread_id: int,
    question: str,
    answer: str,
    *,
    history: list[AnyMessage] | None = None,
    **conf,
):
    await _graph(saver, answer).ainvoke(
        {"messages": [*(history or []), HumanMessage(question)]},
        {"configurable": {"thread_id": str(thread_id), **conf}},
    )


@pytest_asyncio.fixture
async def client(
    db_session: AsyncSession, db_user: User
) -> AsyncGenerator[httpx.AsyncClient, None]:
    async def override_session() -> AsyncGenerator[AsyncSession, None]:
        yield db_session

    async def override_auth() -> AuthContext:
        return AuthContext.session(db_user)

    previous = app.dependency_overrides.copy()
    app.dependency_overrides[get_async_session] = override_session
    app.dependency_overrides[get_auth_context] = override_auth
    try:
        async with httpx.AsyncClient(
            transport=ASGITransport(app=app), base_url="http://test", timeout=60.0
        ) as test_client:
            yield test_client
    finally:
        app.dependency_overrides.clear()
        app.dependency_overrides.update(previous)


@pytest.fixture
def turn_runs(monkeypatch, pg_saver):
    """Replace the turn: record its arguments; optionally write its checkpoints."""
    calls: list[dict[str, Any]] = []
    behaviour = {"reaches_agent": True}

    async def fake_stream_new_chat(**kwargs) -> AsyncGenerator[str, None]:
        calls.append(kwargs)
        if behaviour["reaches_agent"]:
            conf: dict[str, Any] = {"turn_id": f"{kwargs['chat_id']}:rerun{len(calls)}"}
            if kwargs["checkpoint_id"]:
                conf["checkpoint_id"] = kwargs["checkpoint_id"]
            await _run_turn(
                pg_saver,
                kwargs["chat_id"],
                kwargs["user_query"],
                "new answer",
                **conf,
            )
        yield 'data: {"type":"finish"}\n\n'

    monkeypatch.setattr(new_chat_routes, "stream_new_chat", fake_stream_new_chat)
    return calls, behaviour


async def _saved_texts(db_session, thread_id: int) -> list[str]:
    rows = (
        await db_session.execute(
            select(NewChatMessage)
            .where(NewChatMessage.thread_id == thread_id)
            .order_by(NewChatMessage.created_at)
        )
    ).scalars()
    return [row.content[0]["text"] for row in rows]


@pytest_asyncio.fixture
async def two_turns(chat, save_turn, pg_saver):
    """A finished chat: the second question was asked with Google Search."""
    await _run_turn(pg_saver, chat.id, "What is an EVD?", "A drain.", turn_id="t1")
    first_turn_end = await pg_saver.aget_tuple(
        {"configurable": {"thread_id": str(chat.id)}}
    )
    await _run_turn(
        pg_saver,
        chat.id,
        "EVD replacing output?",
        "Infections.",
        turn_id="t2",
        **turn_options_configurable(ToolPreference.GOOGLE_SEARCH),
    )
    await save_turn(chat, "What is an EVD?", "A drain.", turn_id="t1")
    question, answer = await save_turn(
        chat, "EVD replacing output?", "Infections.", turn_id="t2"
    )
    return {
        "question": question,
        "answer": answer,
        "pre_turn": first_turn_end.config["configurable"]["checkpoint_id"],
    }


async def test_regenerate_asks_the_saved_question_from_before_it(
    client,
    db_session,
    db_workspace,
    chat,
    two_turns,
    turn_runs,
    cleanup_in_test_session,
):
    calls, _ = turn_runs

    response = await client.post(
        f"/api/v1/threads/{chat.id}/regenerate",
        json={
            "workspace_id": db_workspace.id,
            "from_message_id": two_turns["question"].id,
        },
    )
    assert response.status_code == 200, response.text
    await response.aread()

    (call,) = calls
    assert call["user_query"] == "EVD replacing output?"
    assert call["checkpoint_id"] == two_turns["pre_turn"]
    assert call["preferred_tool"] is ToolPreference.GOOGLE_SEARCH
    # The tool needs the web specialists, so a documents-only request widens.
    assert call["retrieval_scope"] is RetrievalScope.ALL
    assert call["flow"] == "regenerate"
    assert call["history_before"] == two_turns["question"].created_at
    assert await _saved_texts(db_session, chat.id) == ["What is an EVD?", "A drain."]


async def test_without_a_message_id_the_latest_turn_is_replaced(
    client,
    db_session,
    db_workspace,
    chat,
    two_turns,
    turn_runs,
    cleanup_in_test_session,
):
    calls, _ = turn_runs

    response = await client.post(
        f"/api/v1/threads/{chat.id}/regenerate",
        json={"workspace_id": db_workspace.id},
    )
    assert response.status_code == 200, response.text
    await response.aread()

    assert calls[0]["checkpoint_id"] == two_turns["pre_turn"]
    assert await _saved_texts(db_session, chat.id) == ["What is an EVD?", "A drain."]


async def test_an_edit_keeps_the_tool_of_the_question_it_rewords(
    client, db_workspace, chat, two_turns, turn_runs, cleanup_in_test_session
):
    calls, _ = turn_runs

    response = await client.post(
        f"/api/v1/threads/{chat.id}/regenerate",
        json={
            "workspace_id": db_workspace.id,
            "user_query": "EVD output replacement?",
            "user_images": [],
            "from_message_id": two_turns["question"].id,
        },
    )
    assert response.status_code == 200, response.text
    await response.aread()

    assert calls[0]["user_query"] == "EVD output replacement?"
    assert calls[0]["preferred_tool"] is ToolPreference.GOOGLE_SEARCH


async def test_a_rerun_that_never_reached_the_agent_keeps_the_old_turn(
    client,
    db_session,
    db_workspace,
    chat,
    two_turns,
    turn_runs,
    cleanup_in_test_session,
):
    _, behaviour = turn_runs
    behaviour["reaches_agent"] = False

    response = await client.post(
        f"/api/v1/threads/{chat.id}/regenerate",
        json={"workspace_id": db_workspace.id},
    )
    assert response.status_code == 200, response.text
    await response.aread()

    assert await _saved_texts(db_session, chat.id) == [
        "What is an EVD?",
        "A drain.",
        "EVD replacing output?",
        "Infections.",
    ]


async def test_a_message_of_another_chat_is_not_found(
    client, db_workspace, chat, two_turns, turn_runs
):
    response = await client.post(
        f"/api/v1/threads/{chat.id}/regenerate",
        json={"workspace_id": db_workspace.id, "from_message_id": 987_654_321},
    )

    assert response.status_code == 404


async def test_a_copied_chats_first_question_gets_its_history_on_every_rerun(
    client,
    db_session,
    db_workspace,
    chat,
    save_turn,
    pg_saver,
    turn_runs,
    cleanup_in_test_session,
):
    calls, _ = turn_runs
    # A copied chat: its rows are saved, and its first run was given them
    # as history (which turned needs_history_bootstrap off).
    await save_turn(chat, "copied question", "copied answer", turn_id=None)
    copied = [HumanMessage("copied question"), AIMessage("copied answer")]
    await _run_turn(
        pg_saver, chat.id, "What is an EVD?", "A drain.", turn_id="t1", history=copied
    )
    question, _ = await save_turn(chat, "What is an EVD?", "A drain.", turn_id="t1")

    for n in (1, 2):
        response = await client.post(
            f"/api/v1/threads/{chat.id}/regenerate",
            json={"workspace_id": db_workspace.id, "from_message_id": question.id},
        )
        assert response.status_code == 200, response.text
        await response.aread()
        # The re-run's rows, as the real turn saves them.
        question, _ = await save_turn(
            chat, "What is an EVD?", "new answer", turn_id=f"{chat.id}:rerun{n}"
        )

    first, second = calls
    assert first["needs_history_bootstrap"] is True
    # The second re-run starts from the same empty memory, so it is given
    # the copied rows again.
    assert second["needs_history_bootstrap"] is True
    assert second["checkpoint_id"] == first["checkpoint_id"]
    assert second["history_before"] > first["history_before"]
    assert await _saved_texts(db_session, chat.id) == [
        "copied question",
        "copied answer",
        "What is an EVD?",
        "new answer",
    ]
