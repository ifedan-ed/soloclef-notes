"""The replaced messages go once the re-run moved the agent's memory; usage stays."""

from __future__ import annotations

from typing import Annotated

import pytest
from langchain_core.messages import AIMessage, AnyMessage, HumanMessage
from langgraph.graph import END, START, StateGraph
from langgraph.graph.message import add_messages
from sqlalchemy import select
from typing_extensions import TypedDict

from app.db import ChatComment, NewChatMessage, TokenUsage
from app.tasks.chat.streaming.flows.regenerate import remove_replaced_messages

pytestmark = pytest.mark.integration


class _State(TypedDict):
    messages: Annotated[list[AnyMessage], add_messages]


async def _answer_once(saver, thread_id: int) -> None:
    graph = StateGraph(_State)
    graph.add_node("model", lambda _s: {"messages": [AIMessage("answer")]})
    graph.add_edge(START, "model")
    graph.add_edge("model", END)
    await graph.compile(checkpointer=saver).ainvoke(
        {"messages": [HumanMessage("question")]},
        {"configurable": {"thread_id": str(thread_id)}},
    )


async def _latest_id(saver, thread_id: int) -> str | None:
    latest = await saver.aget_tuple({"configurable": {"thread_id": str(thread_id)}})
    return latest.config["configurable"]["checkpoint_id"] if latest else None


async def _rows(db_session, thread_id: int) -> list[int]:
    return list(
        (
            await db_session.execute(
                select(NewChatMessage.id).where(NewChatMessage.thread_id == thread_id)
            )
        ).scalars()
    )


async def test_once_the_rerun_moved_the_memory_the_rows_go_and_usage_stays(
    db_session,
    db_user,
    db_workspace,
    chat,
    save_turn,
    pg_saver,
    cleanup_in_test_session,
):
    question, answer = await save_turn(chat, "Q", "old answer", turn_id=f"{chat.id}:1")
    usage = TokenUsage(
        prompt_tokens=1200,
        completion_tokens=300,
        total_tokens=1500,
        cost_micros=4200,
        usage_type="chat",
        thread_id=chat.id,
        message_id=answer.id,
        workspace_id=db_workspace.id,
        user_id=db_user.id,
    )
    comment = ChatComment(
        message_id=answer.id, thread_id=chat.id, author_id=db_user.id, content="hm"
    )
    db_session.add_all([usage, comment])
    await db_session.flush()
    before = await _latest_id(pg_saver, chat.id)
    await _answer_once(pg_saver, chat.id)

    await remove_replaced_messages(
        chat.id, [question.id, answer.id], latest_before_rerun=before
    )

    assert await _rows(db_session, chat.id) == []
    await db_session.refresh(usage)
    assert usage.message_id is None
    assert usage.total_tokens == 1500
    assert (
        await db_session.execute(
            select(ChatComment).where(ChatComment.id == comment.id)
        )
    ).scalar_one_or_none() is None


async def test_a_rerun_that_never_reached_the_agent_keeps_the_rows(
    db_session, chat, save_turn, pg_saver, cleanup_in_test_session
):
    question, answer = await save_turn(chat, "Q", "old answer", turn_id=f"{chat.id}:1")
    await _answer_once(pg_saver, chat.id)
    before = await _latest_id(pg_saver, chat.id)

    await remove_replaced_messages(
        chat.id, [question.id, answer.id], latest_before_rerun=before
    )

    assert sorted(await _rows(db_session, chat.id)) == sorted([question.id, answer.id])
