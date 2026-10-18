"""Notebook notes: scratch markdown that can be promoted to a source."""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, Response
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission, get_async_session
from app.users import get_auth_context

from .. import note_conversion
from ..access import authorized_notebook, load_notebook, touch
from ..persistence import Notebook, NotebookNote
from ..schemas import NoteCopy, NoteCreate, NoteRead, NoteUpdate, SourceRead
from ..sources_listing import list_sources

router = APIRouter()

_BASE = "/workspaces/{workspace_id}/notebooks/{notebook_id}/notes"


async def _load_note(
    session: AsyncSession, notebook: Notebook, note_id: int
) -> NotebookNote:
    note = await session.scalar(
        select(NotebookNote).where(
            NotebookNote.id == note_id, NotebookNote.notebook_id == notebook.id
        )
    )
    if note is None:
        raise HTTPException(status_code=404, detail="Note not found")
    return note


@router.get(_BASE, response_model=list[NoteRead])
async def list_notes(
    workspace_id: int,
    notebook_id: int,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_READ
    )
    response.headers["Cache-Control"] = "private, no-store"
    return (
        await session.scalars(
            select(NotebookNote)
            .where(NotebookNote.notebook_id == notebook.id)
            .order_by(NotebookNote.updated_at.desc(), NotebookNote.id.desc())
        )
    ).all()


@router.post(_BASE, response_model=NoteRead, status_code=201)
async def create_note(
    workspace_id: int,
    notebook_id: int,
    body: NoteCreate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_UPDATE
    )
    note = NotebookNote(
        notebook_id=notebook.id,
        title=body.title.strip() or "New note",
        content=body.content,
        created_by_id=auth.user.id,
        origin=body.origin,
    )
    session.add(note)
    touch(notebook)
    await session.commit()
    await session.refresh(note)
    return note


@router.get(_BASE + "/{note_id}", response_model=NoteRead)
async def get_note(
    workspace_id: int,
    notebook_id: int,
    note_id: int,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """One note with its full text (the Notes page opens notes one at a time)."""
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_READ
    )
    response.headers["Cache-Control"] = "private, no-store"
    return await _load_note(session, notebook, note_id)


@router.patch(_BASE + "/{note_id}", response_model=NoteRead)
async def update_note(
    workspace_id: int,
    notebook_id: int,
    note_id: int,
    body: NoteUpdate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_UPDATE
    )
    note = await _load_note(session, notebook, note_id)
    if body.title is not None:
        note.title = body.title.strip() or note.title
    if body.content is not None:
        note.content = body.content
    await session.commit()
    await session.refresh(note)
    return note


@router.delete(_BASE + "/{note_id}", status_code=204)
async def delete_note(
    workspace_id: int,
    notebook_id: int,
    note_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Delete the note; a document it was converted into stays in the workspace."""
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_UPDATE
    )
    note = await _load_note(session, notebook, note_id)
    await session.delete(note)
    await session.commit()
    return Response(status_code=204)


@router.post(_BASE + "/{note_id}/copy", response_model=NoteRead, status_code=201)
async def copy_note(
    workspace_id: int,
    notebook_id: int,
    note_id: int,
    body: NoteCopy,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Put a copy of the note in another notebook of this workspace.

    The copy keeps the title, text, origin and automation; it is not a
    source there even when the original was converted.
    """
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_UPDATE
    )
    note = await _load_note(session, notebook, note_id)
    target = await load_notebook(session, workspace_id, body.notebook_id)
    copy = NotebookNote(
        notebook_id=target.id,
        title=note.title,
        content=note.content,
        created_by_id=auth.user.id,
        origin=note.origin,
        automation_id=note.automation_id,
    )
    session.add(copy)
    touch(target)
    await session.commit()
    await session.refresh(copy)
    return copy


@router.post(_BASE + "/{note_id}/convert", response_model=list[SourceRead])
async def convert_note(
    workspace_id: int,
    notebook_id: int,
    note_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Create a NOTE document from the note and add it to the notebook's sources."""
    notebook = await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_UPDATE,
        Permission.DOCUMENTS_CREATE,
    )
    note = await _load_note(session, notebook, note_id)
    if note.converted_document_id is not None:
        raise HTTPException(
            status_code=409, detail="This note has already been added as a source"
        )
    await note_conversion.convert_note_to_source(
        session, notebook=notebook, note=note, user_id=auth.user.id
    )
    return await list_sources(session, notebook)
