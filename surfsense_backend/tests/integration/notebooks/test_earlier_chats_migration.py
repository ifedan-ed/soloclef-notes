"""c0003 files standalone chats into "Earlier chats", and its downgrade undoes it.

Runs the statements the migration ships (imported from the revision file, so
a drift fails here) against a real database. The session's outer transaction
is rolled back afterwards, which also restores the table the downgrade drops.
"""

from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest
from sqlalchemy import func, select, text

from app.db import (
    ARTIFACT_API_SOURCE,
    ChatVisibility,
    NewChatMessage,
    NewChatMessageRole,
    NewChatThread,
)
from app.notebooks.persistence import Notebook

pytestmark = pytest.mark.integration

_REVISION = (
    Path(__file__).resolve().parents[3]
    / "alembic"
    / "versions"
    / "c0003_earlier_chats_notebooks.py"
)


def _migration():
    spec = importlib.util.spec_from_file_location("c0003_earlier_chats", _REVISION)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


async def _run(db_session, statements) -> None:
    for statement in statements:
        await db_session.execute(text(statement))


async def _thread(db_session, owner, **overrides) -> NewChatThread:
    thread = NewChatThread(
        title=overrides.pop("title", "Chat"),
        workspace_id=owner.workspace.id,
        created_by_id=owner.user.id,
        visibility=ChatVisibility.PRIVATE,
        **overrides,
    )
    db_session.add(thread)
    await db_session.flush()
    for index in range(3):
        db_session.add(
            NewChatMessage(
                thread_id=thread.id,
                role=NewChatMessageRole.USER,
                content=[{"type": "text", "text": f"message {index}"}],
            )
        )
    await db_session.flush()
    return thread


async def _notebook_of(db_session, thread_id: int) -> int | None:
    return await db_session.scalar(
        select(NewChatThread.notebook_id).where(NewChatThread.id == thread_id)
    )


async def _notebook_exists(db_session, notebook_id: int) -> bool:
    return (
        await db_session.scalar(select(Notebook.id).where(Notebook.id == notebook_id))
        is not None
    )


async def _message_count(db_session, thread_id: int) -> int:
    return await db_session.scalar(
        select(func.count())
        .select_from(NewChatMessage)
        .where(NewChatMessage.thread_id == thread_id)
    )


async def test_upgrade_files_standalone_chats_and_downgrade_restores(
    db_session, make_owner
):
    migration = _migration()
    first_owner = await make_owner()
    second_owner = await make_owner()
    research = Notebook(workspace_id=first_owner.workspace.id, title="Research")
    db_session.add(research)
    await db_session.flush()

    old_a = await _thread(db_session, first_owner, title="Greeting")
    old_b = await _thread(db_session, first_owner, title="Quiz")
    in_notebook = await _thread(db_session, first_owner, notebook_id=research.id)
    api_run = await _thread(db_session, first_owner, source=ARTIFACT_API_SOURCE)
    other_workspace = await _thread(db_session, second_owner, title="Elsewhere")

    await _run(db_session, migration.UPGRADE)

    earlier = (
        await db_session.scalars(
            select(Notebook).where(Notebook.title == migration.TITLE)
        )
    ).all()
    by_workspace = {notebook.workspace_id: notebook for notebook in earlier}
    assert set(by_workspace) == {first_owner.workspace.id, second_owner.workspace.id}
    first_earlier = by_workspace[first_owner.workspace.id]
    assert first_earlier.icon == migration.ICON
    assert first_earlier.created_by_id == first_owner.user.id

    assert await _notebook_of(db_session, old_a.id) == first_earlier.id
    assert await _notebook_of(db_session, old_b.id) == first_earlier.id
    assert (
        await _notebook_of(db_session, other_workspace.id)
        == by_workspace[second_owner.workspace.id].id
    )
    # Chats that already had a notebook, and API runs, are untouched.
    assert await _notebook_of(db_session, in_notebook.id) == research.id
    assert await _notebook_of(db_session, api_run.id) is None
    for thread in (old_a, old_b, other_workspace):
        assert await _message_count(db_session, thread.id) == 3

    # A chat started in "Earlier chats" after the move keeps that notebook.
    started_since = await _thread(
        db_session, second_owner, notebook_id=by_workspace[second_owner.workspace.id].id
    )

    await _run(db_session, migration.DOWNGRADE)

    for thread in (old_a, old_b, other_workspace):
        assert await _notebook_of(db_session, thread.id) is None
        assert await _message_count(db_session, thread.id) == 3
    assert await _notebook_of(db_session, in_notebook.id) == research.id
    assert await _notebook_exists(db_session, research.id)
    assert not await _notebook_exists(db_session, first_earlier.id)
    assert (
        await _notebook_of(db_session, started_since.id)
        == by_workspace[second_owner.workspace.id].id
    )
    assert (
        await db_session.scalar(
            text("SELECT to_regclass('notebook_earlier_chats') IS NULL")
        )
        is True
    )


async def test_upgrade_with_nothing_standalone_creates_no_notebook(
    db_session, make_owner
):
    migration = _migration()
    owner = await make_owner()
    await _thread(db_session, owner, source=ARTIFACT_API_SOURCE)

    await _run(db_session, migration.UPGRADE)

    assert (
        await db_session.scalar(
            select(func.count())
            .select_from(Notebook)
            .where(Notebook.workspace_id == owner.workspace.id)
        )
        == 0
    )
