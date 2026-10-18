"""Which documents each notebook of a workspace searches, in two queries.

The same rule a notebook chat's scope uses (``chat/pins.py``): the notebook's
enabled document sources, plus the documents directly inside its enabled
folder sources (one level, not subfolders). Every target must live in the
notebook's own workspace, so a stale or foreign reference is dropped.

Two differences, both narrowing:

* Studio outputs are indexed as ``ARTIFACT`` documents. They are not sources,
  so they never match here even if one sits in a source folder.
* Documents being deleted are left out, as the chat retriever does.
"""

from __future__ import annotations

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, DocumentType, Folder

from ..persistence import Notebook, NotebookSource
from .grouping import SourceLink


def _searchable(workspace_id: int):
    return (
        Document.workspace_id == workspace_id,
        Document.document_type != DocumentType.ARTIFACT,
        func.coalesce(Document.status["state"].astext, "ready") != "deleting",
    )


async def notebook_document_links(
    session: AsyncSession, workspace_id: int
) -> dict[int, list[SourceLink]]:
    """``{document_id: [how it reaches each notebook]}`` for the workspace."""
    direct = await session.execute(
        select(NotebookSource.notebook_id, NotebookSource.id, Document.id)
        .join(Notebook, Notebook.id == NotebookSource.notebook_id)
        .join(Document, Document.id == NotebookSource.document_id)
        .where(
            Notebook.workspace_id == workspace_id,
            NotebookSource.enabled.is_(True),
            *_searchable(workspace_id),
        )
    )
    via_folder = await session.execute(
        select(NotebookSource.notebook_id, NotebookSource.id, Document.id, Folder.name)
        .join(Notebook, Notebook.id == NotebookSource.notebook_id)
        .join(Folder, Folder.id == NotebookSource.folder_id)
        .join(Document, Document.folder_id == Folder.id)
        .where(
            Notebook.workspace_id == workspace_id,
            Folder.workspace_id == workspace_id,
            NotebookSource.enabled.is_(True),
            *_searchable(workspace_id),
        )
    )
    links: dict[int, list[SourceLink]] = {}
    seen: set[tuple[int, int]] = set()
    # Direct sources first: when a document is both a source and inside a
    # source folder, the result links to its own row (which has a guide).
    for notebook_id, source_id, document_id in direct.all():
        seen.add((notebook_id, document_id))
        links.setdefault(document_id, []).append(SourceLink(notebook_id, source_id))
    for notebook_id, source_id, document_id, folder_name in via_folder.all():
        if (notebook_id, document_id) in seen:
            continue
        seen.add((notebook_id, document_id))
        links.setdefault(document_id, []).append(
            SourceLink(notebook_id, source_id, folder_name)
        )
    for entries in links.values():
        entries.sort(key=lambda link: (link.notebook_id, link.source_id))
    return links
