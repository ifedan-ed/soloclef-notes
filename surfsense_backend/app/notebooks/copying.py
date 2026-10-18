"""Duplicate a notebook: its settings, source links and notes.

Chats and Studio outputs stay with the original; a copy starts a fresh
conversation over the same sources. Documents are linked, never duplicated.
"""

from __future__ import annotations

from uuid import UUID

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from .persistence import Notebook, NotebookNote, NotebookSource


async def copy_notebook(
    session: AsyncSession, notebook: Notebook, *, title: str, user_id: UUID
) -> Notebook:
    copy = Notebook(
        workspace_id=notebook.workspace_id,
        title=title[:200],
        icon=notebook.icon,
        created_by_id=user_id,
        chat_goal=notebook.chat_goal,
        chat_instructions=notebook.chat_instructions,
        response_length=notebook.response_length,
    )
    session.add(copy)
    await session.flush()

    sources = (
        await session.scalars(
            select(NotebookSource)
            .where(NotebookSource.notebook_id == notebook.id)
            .order_by(NotebookSource.added_at, NotebookSource.id)
        )
    ).all()
    for source in sources:
        session.add(
            NotebookSource(
                notebook_id=copy.id,
                document_id=source.document_id,
                folder_id=source.folder_id,
                enabled=source.enabled,
                added_at=source.added_at,
            )
        )
    notes = (
        await session.scalars(
            select(NotebookNote)
            .where(NotebookNote.notebook_id == notebook.id)
            .order_by(NotebookNote.created_at, NotebookNote.id)
        )
    ).all()
    for note in notes:
        session.add(
            NotebookNote(
                notebook_id=copy.id,
                title=note.title,
                content=note.content,
                created_by_id=user_id,
                converted_document_id=note.converted_document_id,
                origin=note.origin,
                automation_id=note.automation_id,
            )
        )
    await session.commit()
    await session.refresh(copy)
    return copy
