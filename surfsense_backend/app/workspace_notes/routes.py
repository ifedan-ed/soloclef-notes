"""The Notes page API, mounted under ``/api/v1``.

Listing and moving only: a notebook note is opened, edited, copied, deleted
and converted through its notebook's note routes, and a note document is
edited through the document editor routes and deleted through
``DELETE /workspaces/{id}/notes/{id}``, so each kind keeps one write path.
"""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, Query, Response
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import (
    Document,
    DocumentType,
    Permission,
    get_async_session,
    has_permission,
)
from app.notebooks.access import authorize, load_notebook
from app.notebooks.schemas import NoteRead
from app.users import get_auth_context
from app.utils.rbac import get_user_permissions

from .listing import list_workspace_notes
from .moving import move_into_notebook
from .schemas import MoveIntoNotebook, WorkspaceNoteRead

router = APIRouter(tags=["notes"])

_BASE = "/workspaces/{workspace_id}/all-notes"


@router.get(_BASE, response_model=list[WorkspaceNoteRead])
async def list_notes(
    workspace_id: int,
    response: Response,
    q: str | None = Query(default=None),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Every note the caller can see in the workspace, newest first."""
    await authorize(session, auth, workspace_id, Permission.CHATS_READ)
    permissions = await get_user_permissions(session, auth.user.id, workspace_id)
    response.headers["Cache-Control"] = "private, no-store"
    return await list_workspace_notes(
        session,
        workspace_id,
        query=q,
        include_documents=has_permission(permissions, Permission.DOCUMENTS_READ.value),
    )


@router.post(
    _BASE + "/documents/{document_id}/move",
    response_model=NoteRead,
    status_code=201,
)
async def move_note_document(
    workspace_id: int,
    document_id: int,
    body: MoveIntoNotebook,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Make a note document a note of one of this workspace's notebooks.

    Needs notebook and document editing (Owners and Editors). The document
    is deleted only for a caller who may delete documents (Owners by
    default); for anyone else the note is a copy and the document stays,
    since the note can then be deleted with notebook rights alone.
    """
    await authorize(
        session,
        auth,
        workspace_id,
        Permission.CHATS_UPDATE,
        Permission.DOCUMENTS_UPDATE,
    )
    permissions = await get_user_permissions(session, auth.user.id, workspace_id)
    may_delete = has_permission(permissions, Permission.DOCUMENTS_DELETE.value)
    notebook = await load_notebook(session, workspace_id, body.notebook_id)
    document = await session.scalar(
        select(Document).where(
            Document.id == document_id,
            Document.workspace_id == workspace_id,
            Document.document_type == DocumentType.NOTE,
        )
    )
    if document is None:
        raise HTTPException(status_code=404, detail="Note not found")
    return await move_into_notebook(
        session,
        document=document,
        notebook=notebook,
        moved_by=auth.user.id,
        keep_document=not may_delete,
    )
