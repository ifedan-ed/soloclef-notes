"""The source guide shown when a source is opened in the notebook."""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Document, Permission, get_async_session
from app.users import get_auth_context

from .. import source_guide as guides
from ..access import authorized_notebook
from ..persistence import NotebookSource
from ..schemas import SourceGuideRead

router = APIRouter()


@router.get(
    "/workspaces/{workspace_id}/notebooks/{notebook_id}/sources/{source_id}/guide",
    response_model=SourceGuideRead,
)
async def get_source_guide(
    workspace_id: int,
    notebook_id: int,
    source_id: int,
    locale: str | None = Query(
        None, max_length=35, description="The UI locale the guide is written in"
    ),
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
    document = await session.scalar(
        select(Document)
        .join(NotebookSource, NotebookSource.document_id == Document.id)
        .where(
            NotebookSource.id == source_id,
            NotebookSource.notebook_id == notebook.id,
            Document.workspace_id == workspace_id,
        )
    )
    if document is None:
        raise HTTPException(status_code=404, detail="Source not found")
    summary = await guides.source_guide(session, document, locale=locale)
    return SourceGuideRead(
        document_id=document.id,
        title=document.title,
        summary=summary,
        status="ready" if summary else "unavailable",
    )
