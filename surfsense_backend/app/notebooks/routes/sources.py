"""Add, remove and toggle a notebook's sources."""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, Response
from sqlalchemy import delete, select, update
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Document, Folder, Permission, get_async_session
from app.users import get_auth_context

from ..access import authorized_notebook, touch
from ..add_sources import link_sources
from ..persistence import NotebookSource
from ..schemas import SourceRead, SourcesAdd, SourcesBulkToggle, SourceToggle
from ..sources_listing import list_sources

router = APIRouter()

_BASE = "/workspaces/{workspace_id}/notebooks/{notebook_id}/sources"


@router.get(_BASE, response_model=list[SourceRead])
async def get_sources(
    workspace_id: int,
    notebook_id: int,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_READ,
        Permission.DOCUMENTS_READ,
    )
    response.headers["Cache-Control"] = "private, no-store"
    return await list_sources(session, notebook)


@router.post(_BASE, response_model=list[SourceRead])
async def add_sources(
    workspace_id: int,
    notebook_id: int,
    body: SourcesAdd,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Link documents/folders; every target must belong to this workspace."""
    notebook = await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_UPDATE,
        Permission.DOCUMENTS_READ,
    )
    doc_ids = sorted({t.id for t in body.targets if t.kind == "document"})
    folder_ids = sorted({t.id for t in body.targets if t.kind == "folder"})

    found_docs = set(
        (
            await session.scalars(
                select(Document.id).where(
                    Document.id.in_(doc_ids), Document.workspace_id == workspace_id
                )
            )
        ).all()
    )
    found_folders = set(
        (
            await session.scalars(
                select(Folder.id).where(
                    Folder.id.in_(folder_ids), Folder.workspace_id == workspace_id
                )
            )
        ).all()
    )
    if found_docs != set(doc_ids) or found_folders != set(folder_ids):
        # Same answer for "does not exist" and "belongs to someone else".
        raise HTTPException(status_code=404, detail="Source not found")

    await link_sources(session, notebook, document_ids=doc_ids, folder_ids=folder_ids)
    await session.commit()
    return await list_sources(session, notebook)


@router.patch(_BASE + "/{source_id}", response_model=SourceRead)
async def toggle_source(
    workspace_id: int,
    notebook_id: int,
    source_id: int,
    body: SourceToggle,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_UPDATE
    )
    source = await session.scalar(
        select(NotebookSource).where(
            NotebookSource.id == source_id,
            NotebookSource.notebook_id == notebook.id,
        )
    )
    if source is None:
        raise HTTPException(status_code=404, detail="Source not found")
    source.enabled = body.enabled
    await session.commit()
    for item in await list_sources(session, notebook):
        if item.id == source_id:
            return item
    raise HTTPException(status_code=404, detail="Source not found")


@router.post(_BASE + "/toggle", response_model=list[SourceRead])
async def toggle_sources(
    workspace_id: int,
    notebook_id: int,
    body: SourcesBulkToggle,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Select or clear all sources (or the listed subset) in one write."""
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_UPDATE
    )
    statement = (
        update(NotebookSource)
        .where(NotebookSource.notebook_id == notebook.id)
        .values(enabled=body.enabled)
    )
    if body.source_ids is not None:
        statement = statement.where(NotebookSource.id.in_(body.source_ids))
    await session.execute(statement)
    await session.commit()
    return await list_sources(session, notebook)


@router.delete(_BASE + "/{source_id}", status_code=204)
async def remove_source(
    workspace_id: int,
    notebook_id: int,
    source_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Unlink a source; the document itself is untouched."""
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_UPDATE
    )
    result = await session.execute(
        delete(NotebookSource).where(
            NotebookSource.id == source_id,
            NotebookSource.notebook_id == notebook.id,
        )
    )
    if result.rowcount == 0:
        raise HTTPException(status_code=404, detail="Source not found")
    touch(notebook)
    await session.commit()
    return Response(status_code=204)
