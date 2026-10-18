"""Chats that belong to a notebook."""

from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter, Depends, Response
from sqlalchemy import or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import (
    ARTIFACT_API_SOURCE,
    ChatVisibility,
    NewChatThread,
    Permission,
    Workspace,
    get_async_session,
)
from app.users import get_auth_context

from ..access import authorized_notebook, touch
from ..schemas import NotebookThreadRead

router = APIRouter()

_BASE = "/workspaces/{workspace_id}/notebooks/{notebook_id}/threads"


@router.get(_BASE, response_model=list[NotebookThreadRead])
async def list_notebook_threads(
    workspace_id: int,
    notebook_id: int,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """The notebook's chats the caller may see, newest first.

    Same visibility rule as the main chat list: your own chats plus chats
    shared with the workspace. Studio's generation threads are excluded.
    """
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_READ
    )
    workspace_owner_id = await session.scalar(
        select(Workspace.user_id).where(Workspace.id == workspace_id)
    )
    visible = [
        NewChatThread.created_by_id == auth.user.id,
        NewChatThread.visibility == ChatVisibility.SEARCH_SPACE,
    ]
    if workspace_owner_id == auth.user.id:
        visible.append(NewChatThread.created_by_id.is_(None))
    threads = (
        await session.scalars(
            select(NewChatThread)
            .where(
                NewChatThread.notebook_id == notebook.id,
                NewChatThread.workspace_id == workspace_id,
                NewChatThread.source != ARTIFACT_API_SOURCE,
                NewChatThread.archived.is_(False),
                or_(*visible),
            )
            .order_by(NewChatThread.updated_at.desc(), NewChatThread.id.desc())
        )
    ).all()
    response.headers["Cache-Control"] = "private, no-store"
    return [
        NotebookThreadRead(
            id=t.id, title=t.title, created_at=t.created_at, updated_at=t.updated_at
        )
        for t in threads
    ]


@router.post(_BASE, response_model=NotebookThreadRead, status_code=201)
async def create_notebook_thread(
    workspace_id: int,
    notebook_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Start a chat bound to the notebook; its sources become the chat's pins."""
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_CREATE
    )
    now = datetime.now(UTC)
    thread = NewChatThread(
        title="New Chat",
        workspace_id=workspace_id,
        created_by_id=auth.user.id,
        visibility=ChatVisibility.PRIVATE,
        notebook_id=notebook.id,
        updated_at=now,
    )
    session.add(thread)
    touch(notebook)
    await session.commit()
    await session.refresh(thread)
    return NotebookThreadRead(
        id=thread.id,
        title=thread.title,
        created_at=thread.created_at,
        updated_at=thread.updated_at,
    )


@router.delete(_BASE, status_code=204)
async def delete_notebook_chat_history(
    workspace_id: int,
    notebook_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Delete the caller's own chats in this notebook.

    Chat history is private to each user, so only chats the caller started
    go; chats shared with the workspace by others and Studio's generation
    threads (which own the outputs' attribution) are left alone.
    """
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_DELETE
    )
    threads = (
        await session.scalars(
            select(NewChatThread).where(
                NewChatThread.notebook_id == notebook.id,
                NewChatThread.workspace_id == workspace_id,
                NewChatThread.created_by_id == auth.user.id,
                NewChatThread.source != ARTIFACT_API_SOURCE,
            )
        )
    ).all()
    deleted = [thread.id for thread in threads]
    for thread in threads:
        await session.delete(thread)
    await session.commit()

    from app.routes.new_chat_routes import _try_delete_sandbox

    for thread_id in deleted:
        _try_delete_sandbox(thread_id)
    return Response(status_code=204)
