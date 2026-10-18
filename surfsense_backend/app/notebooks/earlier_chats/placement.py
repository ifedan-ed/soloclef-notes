"""File a chat that has no notebook into the workspace's "Earlier chats"."""

from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import ARTIFACT_API_SOURCE, NewChatThread, Workspace

from ..persistence import Notebook
from .model import NotebookEarlierChat

# The migration that first filed these chats writes the same values.
EARLIER_CHATS_TITLE = "Earlier chats"
EARLIER_CHATS_ICON = "\U0001f4ac"


async def _earlier_chats_notebook(session: AsyncSession, workspace_id: int) -> Notebook:
    existing = await session.scalar(
        select(Notebook)
        .join(NotebookEarlierChat, NotebookEarlierChat.notebook_id == Notebook.id)
        .where(NotebookEarlierChat.workspace_id == workspace_id)
        .order_by(Notebook.id)
        .limit(1)
    )
    if existing is not None:
        return existing
    owner_id = await session.scalar(
        select(Workspace.user_id).where(Workspace.id == workspace_id)
    )
    notebook = Notebook(
        workspace_id=workspace_id,
        title=EARLIER_CHATS_TITLE,
        icon=EARLIER_CHATS_ICON,
        created_by_id=owner_id,
    )
    session.add(notebook)
    await session.flush()
    return notebook


async def place_thread(session: AsyncSession, thread: NewChatThread) -> int | None:
    """The notebook the chat opens in, filing it first if it has none.

    API-run threads are machinery, not conversations, and are never filed.
    The caller commits.
    """
    if thread.notebook_id is not None:
        return thread.notebook_id
    if thread.source == ARTIFACT_API_SOURCE:
        return None
    notebook = await _earlier_chats_notebook(session, thread.workspace_id)
    thread.notebook_id = notebook.id
    session.add(
        NotebookEarlierChat(
            thread_id=thread.id,
            notebook_id=notebook.id,
            workspace_id=thread.workspace_id,
        )
    )
    await session.flush()
    return notebook.id
