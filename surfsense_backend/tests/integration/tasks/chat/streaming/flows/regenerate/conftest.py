"""A chat with saved turns, and the real Postgres checkpointer, for Regenerate."""

from __future__ import annotations

from contextlib import asynccontextmanager
from datetime import UTC, datetime, timedelta

import pytest
import pytest_asyncio
from sqlalchemy.ext.asyncio import AsyncSession

from app.agents.chat.runtime import checkpointer as checkpointer_module
from app.db import (
    ChatVisibility,
    NewChatMessage,
    NewChatMessageRole,
    NewChatThread,
    User,
    Workspace,
)
from app.tasks.chat.streaming.flows.regenerate import (
    replaced_messages as replaced_messages_module,
)


@pytest_asyncio.fixture
async def chat(
    db_session: AsyncSession, db_user: User, db_workspace: Workspace
) -> NewChatThread:
    thread = NewChatThread(
        title="EVD",
        workspace_id=db_workspace.id,
        created_by_id=db_user.id,
        visibility=ChatVisibility.PRIVATE,
    )
    db_session.add(thread)
    await db_session.flush()
    return thread


@pytest.fixture
def save_turn(db_session: AsyncSession, db_user: User):
    """Save a question and its answer the way a finished turn leaves them."""
    clock = {"at": datetime(2026, 10, 9, 12, 0, tzinfo=UTC)}

    async def save(
        thread: NewChatThread, question: str, answer: str, *, turn_id: str | None
    ) -> tuple[NewChatMessage, NewChatMessage]:
        rows = []
        for role, content in (
            (NewChatMessageRole.USER, [{"type": "text", "text": question}]),
            (NewChatMessageRole.ASSISTANT, [{"type": "text", "text": answer}]),
        ):
            clock["at"] += timedelta(seconds=5)
            row = NewChatMessage(
                thread_id=thread.id,
                role=role,
                content=content,
                author_id=db_user.id,
                turn_id=turn_id,
                created_at=clock["at"],
            )
            db_session.add(row)
            rows.append(row)
        await db_session.flush()
        return rows[0], rows[1]

    return save


@pytest_asyncio.fixture(scope="session", autouse=True)
async def _checkpoint_tables():
    """Create LangGraph's tables before any test opens its transaction.

    Their setup builds indexes ``CONCURRENTLY``, which waits for every open
    transaction, so it would wait forever on the test's own ``db_session``.
    """
    await checkpointer_module.get_checkpointer()
    yield
    await checkpointer_module.close_checkpointer()


@pytest_asyncio.fixture
async def pg_saver():
    """The checkpointer real turns use (``AsyncPostgresSaver`` on the test DB)."""
    return await checkpointer_module.get_checkpointer()


@pytest.fixture
def cleanup_in_test_session(monkeypatch, db_session: AsyncSession):
    """Run the replaced-message cleanup in the test's rolled-back transaction."""

    @asynccontextmanager
    async def _session():
        yield db_session

    monkeypatch.setattr(replaced_messages_module, "shielded_async_session", _session)
    return db_session
