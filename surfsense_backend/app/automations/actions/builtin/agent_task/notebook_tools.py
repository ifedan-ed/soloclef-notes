"""Tools that let an automation run find the workspace's notebooks and their sources.

A notebook is not a file: the ``/documents`` tree the knowledge-base agent
browses holds documents, including the files Studio writes ("Quiz_ Untitled
notebook", "Mind Map_ ..."). Asked to "list my notebooks", an agent with only
that tree listed those Studio files. These tools answer from the notebooks
tables instead, and give each source's document path so the agent can read a
notebook's sources through the knowledge-base agent.

Both are read-only and bound to the run's workspace; every query also requires
the rows to be in that workspace, so a notebook id from elsewhere is simply
"not found".
"""

from __future__ import annotations

import json
from typing import Annotated, Any

from langchain_core.tools import BaseTool, StructuredTool
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

LIST_NOTEBOOKS = "list_notebooks"
LIST_NOTEBOOK_SOURCES = "list_notebook_sources"
NOTEBOOK_TOOL_NAMES = frozenset({LIST_NOTEBOOKS, LIST_NOTEBOOK_SOURCES})

MAX_NOTEBOOKS = 200
MAX_SOURCES = 200


async def notebooks_in(
    session: AsyncSession, workspace_id: int
) -> list[dict[str, Any]]:
    """Every notebook in the workspace, most recently active first."""
    from app.notebooks.persistence import Notebook, NotebookOutput, NotebookSource

    sources = (
        select(func.count(NotebookSource.id))
        .where(NotebookSource.notebook_id == Notebook.id)
        .correlate(Notebook)
        .scalar_subquery()
    )
    enabled = (
        select(func.count(NotebookSource.id))
        .where(
            NotebookSource.notebook_id == Notebook.id,
            NotebookSource.enabled.is_(True),
        )
        .correlate(Notebook)
        .scalar_subquery()
    )
    outputs = (
        select(func.count(NotebookOutput.id))
        .where(NotebookOutput.notebook_id == Notebook.id)
        .correlate(Notebook)
        .scalar_subquery()
    )
    rows = await session.execute(
        select(
            Notebook.id,
            Notebook.title,
            Notebook.updated_at,
            sources.label("sources"),
            enabled.label("enabled"),
            outputs.label("outputs"),
        )
        .where(Notebook.workspace_id == workspace_id)
        .order_by(Notebook.updated_at.desc(), Notebook.id.desc())
        .limit(MAX_NOTEBOOKS)
    )
    return [
        {
            "notebook_id": row.id,
            "title": row.title,
            "sources": int(row.sources or 0),
            "enabled_sources": int(row.enabled or 0),
            "studio_outputs": int(row.outputs or 0),
            "last_active": row.updated_at.isoformat() if row.updated_at else None,
        }
        for row in rows.all()
    ]


async def notebook_sources(
    session: AsyncSession, workspace_id: int, notebook_id: int
) -> dict[str, Any] | None:
    """The notebook's sources with their document paths, or ``None`` if it is
    not a notebook in this workspace."""
    from app.db import Document, Folder
    from app.knowledge_store.paths import build_path_index, virtual_path_of
    from app.notebooks.persistence import Notebook, NotebookSource

    notebook = await session.scalar(
        select(Notebook).where(
            Notebook.id == notebook_id, Notebook.workspace_id == workspace_id
        )
    )
    if notebook is None:
        return None
    index = await build_path_index(session, workspace_id)
    documents = await session.execute(
        select(NotebookSource.enabled, NotebookSource.added_at, Document)
        .join(Document, Document.id == NotebookSource.document_id)
        .where(
            NotebookSource.notebook_id == notebook.id,
            Document.workspace_id == workspace_id,
        )
        .order_by(NotebookSource.added_at, NotebookSource.id)
        .limit(MAX_SOURCES)
    )
    items: list[dict[str, Any]] = []
    for enabled, added_at, document in documents.all():
        metadata = document.document_metadata or {}
        items.append(
            {
                "kind": "document",
                "document_id": document.id,
                "title": document.title,
                "type": getattr(document.document_type, "value", None)
                or str(document.document_type),
                "url": metadata.get("url") if isinstance(metadata, dict) else None,
                "path": virtual_path_of(
                    path=document.path,
                    doc_id=document.id,
                    title=str(document.title or "untitled"),
                    folder_id=document.folder_id,
                    index=index,
                ),
                "enabled": bool(enabled),
                "added_at": added_at.isoformat() if added_at else None,
            }
        )
    folders = await session.execute(
        select(NotebookSource.enabled, Folder.id, Folder.name)
        .join(Folder, Folder.id == NotebookSource.folder_id)
        .where(
            NotebookSource.notebook_id == notebook.id,
            Folder.workspace_id == workspace_id,
        )
    )
    for enabled, folder_id, name in folders.all():
        items.append(
            {
                "kind": "folder",
                "folder_id": folder_id,
                "title": name,
                "enabled": bool(enabled),
            }
        )
    return {"notebook_id": notebook.id, "title": notebook.title, "sources": items}


def create_notebook_tools(*, workspace_id: int) -> list[BaseTool]:
    """``list_notebooks`` and ``list_notebook_sources`` for one workspace."""

    async def _list_notebooks() -> str:
        from app.db import shielded_async_session

        async with shielded_async_session() as session:
            notebooks = await notebooks_in(session, workspace_id)
        if not notebooks:
            return "This workspace has no notebooks."
        return json.dumps({"notebooks": notebooks}, ensure_ascii=False)

    async def _list_notebook_sources(
        notebook_id: Annotated[int, "The notebook_id from list_notebooks."],
    ) -> str:
        from app.db import shielded_async_session

        async with shielded_async_session() as session:
            found = await notebook_sources(session, workspace_id, int(notebook_id))
        if found is None:
            return (
                f"There is no notebook {notebook_id} in this workspace. "
                "Call list_notebooks for the notebook ids."
            )
        return json.dumps(found, ensure_ascii=False)

    return [
        StructuredTool.from_function(
            name=LIST_NOTEBOOKS,
            description=(
                "List the notebooks in this workspace: id, title, how many "
                "sources each has (and how many are switched on), how many "
                "Studio outputs, and when it was last active. Notebooks are "
                "not files: use this, not the /documents tree, whenever the "
                "task mentions notebooks. Files named like 'Quiz_ ...', "
                "'Mind Map_ ...' or 'Report_ ...' in /documents are Studio "
                "outputs, not notebooks."
            ),
            coroutine=_list_notebooks,
        ),
        StructuredTool.from_function(
            name=LIST_NOTEBOOK_SOURCES,
            description=(
                "List one notebook's sources: title, type, original URL, "
                "whether it is switched on, and the document path. To read "
                "or search a source, delegate to the knowledge_base agent "
                "and give it the path(s) from here."
            ),
            coroutine=_list_notebook_sources,
        ),
    ]
