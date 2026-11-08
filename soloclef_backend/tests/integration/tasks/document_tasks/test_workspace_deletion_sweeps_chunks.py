"""Deleting a workspace takes its chunks, and only its chunks.

The batched sweep in ``_delete_workspace_background`` had no test at all, which
is why it is pinned here before ``Chunk.workspace_id`` replaces the join to
``documents`` that used to find the ids: the two agree only while every chunk
carries its own document's workspace, and that invariant is the whole point of
the column.
"""

from __future__ import annotations

import contextlib
import uuid
from collections.abc import AsyncIterator

import pytest
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.config import config
from app.db import Chunk, Document, DocumentType, User, Workspace
from app.routes.workspaces_routes import create_default_roles_and_membership
from app.tasks.celery_tasks.document_tasks import _delete_workspace_background

pytestmark = pytest.mark.integration

_EMBEDDING = [0.1] * config.embedding_model_instance.dimension


@pytest.fixture
def bind_task_session(db_session: AsyncSession, monkeypatch) -> AsyncSession:
    """Hand the task body the test transaction instead of a fresh session."""

    def _make_session():
        @contextlib.asynccontextmanager
        async def _ctx() -> AsyncIterator[AsyncSession]:
            yield db_session

        return _ctx()

    monkeypatch.setattr(
        "app.tasks.celery_tasks.document_tasks.get_celery_session_maker",
        lambda: _make_session,
    )
    return db_session


async def _add_document_with_chunks(
    session: AsyncSession, *, workspace: Workspace, owner: User, count: int
) -> None:
    uid = uuid.uuid4().hex[:12]
    document = Document(
        title=f"Doc {uid}",
        document_type=DocumentType.FILE,
        content="content",
        content_hash=f"content-{uid}",
        unique_identifier_hash=f"uid-{uid}",
        source_markdown="content",
        workspace_id=workspace.id,
        created_by_id=str(owner.id),
        embedding=_EMBEDDING,
        status={"state": "ready"},
    )
    session.add(document)
    await session.flush()
    session.add_all(
        Chunk(
            content=f"chunk {position}",
            document_id=document.id,
            workspace_id=workspace.id,
            position=position,
            embedding=_EMBEDDING,
        )
        for position in range(count)
    )
    await session.flush()


async def _chunk_count(session: AsyncSession, workspace_id: int) -> int:
    return await session.scalar(
        select(func.count())
        .select_from(Chunk)
        .where(Chunk.workspace_id == workspace_id)
    )


async def test_the_sweep_empties_the_deleted_workspace_and_spares_the_other(
    bind_task_session: AsyncSession,
    db_user: User,
    db_workspace: Workspace,
    make_user,
):
    host = await make_user()
    neighbour = Workspace(name="Neighbour Space", user_id=host.id)
    bind_task_session.add(neighbour)
    await bind_task_session.flush()
    await create_default_roles_and_membership(bind_task_session, neighbour.id, host.id)

    await _add_document_with_chunks(
        bind_task_session, workspace=db_workspace, owner=db_user, count=3
    )
    await _add_document_with_chunks(
        bind_task_session, workspace=neighbour, owner=host, count=2
    )
    doomed, spared = db_workspace.id, neighbour.id

    await _delete_workspace_background(doomed)

    assert await _chunk_count(bind_task_session, doomed) == 0
    assert await _chunk_count(bind_task_session, spared) == 2
    assert (
        await bind_task_session.scalar(
            select(Workspace.id).where(Workspace.id == doomed)
        )
        is None
    )
