"""A notebook's sources joined to the documents and folders they point at."""

from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, Folder

from .persistence import Notebook, NotebookSource
from .schemas import SourceRead


async def list_sources(session: AsyncSession, notebook: Notebook) -> list[SourceRead]:
    """Sources in the order they were added.

    The joins also require the target to live in the notebook's workspace, so a
    row that somehow pointed elsewhere would be invisible rather than leak a
    title.
    """
    doc_rows = await session.execute(
        select(NotebookSource, Document.title, Document.document_type, Document.status)
        .join(Document, Document.id == NotebookSource.document_id)
        .where(
            NotebookSource.notebook_id == notebook.id,
            Document.workspace_id == notebook.workspace_id,
        )
    )
    folder_rows = await session.execute(
        select(NotebookSource, Folder.name)
        .join(Folder, Folder.id == NotebookSource.folder_id)
        .where(
            NotebookSource.notebook_id == notebook.id,
            Folder.workspace_id == notebook.workspace_id,
        )
    )
    items: list[SourceRead] = []
    for source, title, document_type, status in doc_rows.all():
        items.append(
            SourceRead(
                id=source.id,
                kind="document",
                target_id=source.document_id,
                title=title,
                document_type=getattr(document_type, "value", document_type),
                status=(status or {}).get("state")
                if isinstance(status, dict)
                else None,
                enabled=source.enabled,
                added_at=source.added_at,
            )
        )
    for source, name in folder_rows.all():
        items.append(
            SourceRead(
                id=source.id,
                kind="folder",
                target_id=source.folder_id,
                title=name,
                enabled=source.enabled,
                added_at=source.added_at,
            )
        )
    items.sort(key=lambda item: (item.added_at, item.id))
    return items
