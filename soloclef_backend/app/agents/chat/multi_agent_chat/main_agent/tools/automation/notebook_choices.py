"""The notebooks a drafted automation may save its results into.

An automation keeps its result as a note through ``save_to_notebook_id`` (a
notebook), never through a folder. The drafter is told which notebooks the
workspace has, and which one this chat belongs to, so "save it as a note"
becomes that setting instead of an instruction to write a file nobody sees.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession


@dataclass(frozen=True)
class NotebookChoice:
    id: int
    title: str


@dataclass(frozen=True)
class NotebookChoices:
    notebooks: list[NotebookChoice]
    # The notebook this chat belongs to (every chat is a notebook's now).
    current: NotebookChoice | None = None


def chat_thread_id(config: Any) -> int | None:
    """The chat thread a tool call runs in, from its run config."""
    configurable = (config or {}).get("configurable") or {}
    value = configurable.get("thread_id")
    if isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value
    if isinstance(value, str) and value.isdigit():
        return int(value)
    return None


async def load_notebook_choices(
    session: AsyncSession, *, workspace_id: int, thread_id: int | None
) -> NotebookChoices:
    """Every notebook in the workspace (most recently active first), and this
    chat's own. No cap: the drafter needs the one the person names."""
    from app.db import NewChatThread
    from app.notebooks.persistence import Notebook

    rows = (
        await session.execute(
            select(Notebook.id, Notebook.title)
            .where(Notebook.workspace_id == workspace_id)
            .order_by(Notebook.updated_at.desc(), Notebook.id.desc())
        )
    ).all()
    notebooks = [NotebookChoice(id=row.id, title=row.title) for row in rows]
    current = None
    if thread_id is not None:
        notebook_id = await session.scalar(
            select(NewChatThread.notebook_id).where(
                NewChatThread.id == thread_id,
                NewChatThread.workspace_id == workspace_id,
            )
        )
        current = next((n for n in notebooks if n.id == notebook_id), None)
    return NotebookChoices(notebooks=notebooks, current=current)
