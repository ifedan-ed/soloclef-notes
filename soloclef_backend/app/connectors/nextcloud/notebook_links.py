"""Link imported documents and folders to the notebook the import started from."""

from __future__ import annotations

from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.ext.asyncio import AsyncSession

from app.notebooks.persistence import NotebookSource


async def link_document(
    session: AsyncSession, notebook_id: int, document_id: int
) -> None:
    """Link it, or switch an existing, switched-off link back on."""
    await session.execute(
        pg_insert(NotebookSource)
        .values(notebook_id=notebook_id, document_id=document_id, enabled=True)
        .on_conflict_do_update(
            constraint="uq_notebook_sources_document", set_={"enabled": True}
        )
    )


async def link_folder(session: AsyncSession, notebook_id: int, folder_id: int) -> None:
    await session.execute(
        pg_insert(NotebookSource)
        .values(notebook_id=notebook_id, folder_id=folder_id, enabled=True)
        .on_conflict_do_update(
            constraint="uq_notebook_sources_folder", set_={"enabled": True}
        )
    )
