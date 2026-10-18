"""The Notes page's list: every note in a workspace, newest first.

Every notebook note, plus every note document no notebook note stands for (a
converted note's document is listed once, as its note). No cap: a workspace's
notes are few enough to send at once, and the page filters them as you type.
Each note says where it lives, its notebook or its folder, which the page
groups by.
"""

from __future__ import annotations

from sqlalchemy import func, or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.persistence.models.automation import Automation
from app.db import Document, DocumentType
from app.notebooks.persistence import Notebook, NotebookNote

from .folder_places import folder_places
from .note_documents import (
    PREVIEW_CHARS,
    automation_id_of,
    automation_name_of,
    display_title,
    origin_of,
    preview_of,
)
from .schemas import AutomationRef, NotebookRef, WorkspaceNoteRead
from .source_usage import notebooks_using

# Enough of a note's start to build its preview (marks are stripped after).
_PREVIEW_SOURCE_CHARS = PREVIEW_CHARS * 3


def _contains(query: str) -> str:
    """An ILIKE pattern matching ``query`` anywhere, its wildcards escaped."""
    escaped = query.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
    return f"%{escaped}%"


async def _notebook_notes(
    session: AsyncSession, workspace_id: int, query: str | None
) -> list[WorkspaceNoteRead]:
    stmt = (
        select(
            NotebookNote.id,
            NotebookNote.title,
            func.left(NotebookNote.content, _PREVIEW_SOURCE_CHARS).label("start"),
            NotebookNote.origin,
            NotebookNote.automation_id,
            NotebookNote.converted_document_id,
            NotebookNote.created_at,
            NotebookNote.updated_at,
            Notebook.id.label("notebook_id"),
            Notebook.title.label("notebook_title"),
            Notebook.icon.label("notebook_icon"),
            Automation.name.label("automation_name"),
        )
        .join(Notebook, Notebook.id == NotebookNote.notebook_id)
        .outerjoin(Automation, Automation.id == NotebookNote.automation_id)
        .where(Notebook.workspace_id == workspace_id)
    )
    if query:
        pattern = _contains(query)
        stmt = stmt.where(
            or_(
                NotebookNote.title.ilike(pattern, escape="\\"),
                NotebookNote.content.ilike(pattern, escape="\\"),
            )
        )
    notes: list[WorkspaceNoteRead] = []
    for row in (await session.execute(stmt)).all():
        notebook = NotebookRef(
            id=row.notebook_id, title=row.notebook_title, icon=row.notebook_icon
        )
        automation = None
        if row.automation_name is not None:
            automation = AutomationRef(id=row.automation_id, name=row.automation_name)
        notes.append(
            WorkspaceNoteRead(
                kind="notebook_note",
                id=row.id,
                title=row.title,
                preview=preview_of(row.start),
                origin=row.origin,
                notebook=notebook,
                source_in=[notebook] if row.converted_document_id else [],
                automation=automation,
                created_at=row.created_at,
                updated_at=row.updated_at,
            )
        )
    return notes


async def _note_documents(
    session: AsyncSession, workspace_id: int, query: str | None
) -> list[WorkspaceNoteRead]:
    stood_for = (
        select(NotebookNote.converted_document_id)
        .join(Notebook, Notebook.id == NotebookNote.notebook_id)
        .where(
            Notebook.workspace_id == workspace_id,
            NotebookNote.converted_document_id.is_not(None),
        )
    )
    text = func.coalesce(Document.source_markdown, Document.content, "")
    stmt = select(
        Document.id,
        Document.title,
        Document.folder_id,
        Document.document_metadata,
        func.left(text, _PREVIEW_SOURCE_CHARS).label("start"),
        Document.created_at,
        Document.updated_at,
    ).where(
        Document.workspace_id == workspace_id,
        Document.document_type == DocumentType.NOTE,
        Document.id.not_in(stood_for),
    )
    if query:
        pattern = _contains(query)
        stmt = stmt.where(
            or_(
                Document.title.ilike(pattern, escape="\\"),
                text.ilike(pattern, escape="\\"),
            )
        )
    rows = (await session.execute(stmt)).all()
    if not rows:
        return []

    used = await notebooks_using(
        session, workspace_id, [(row.id, row.folder_id) for row in rows]
    )
    folders = await folder_places(session, workspace_id)
    automation_ids = {
        a for a in (automation_id_of(row.document_metadata) for row in rows) if a
    }
    live_names: dict[int, str] = {}
    if automation_ids:
        live_names = dict(
            (
                await session.execute(
                    select(Automation.id, Automation.name).where(
                        Automation.id.in_(automation_ids),
                        Automation.workspace_id == workspace_id,
                    )
                )
            ).all()
        )

    notes: list[WorkspaceNoteRead] = []
    for row in rows:
        meta = row.document_metadata or {}
        automation = None
        if origin_of(meta) == "automation":
            automation_id = automation_id_of(meta)
            # The automation's current name; the one recorded at the run if it
            # has since been deleted.
            if automation_id in live_names:
                automation = AutomationRef(
                    id=automation_id, name=live_names[automation_id]
                )
            else:
                automation = AutomationRef(
                    id=None, name=automation_name_of(meta) or "Automation"
                )
        notes.append(
            WorkspaceNoteRead(
                kind="document",
                id=row.id,
                title=display_title(row.title),
                preview=preview_of(row.start),
                origin=origin_of(meta),
                source_in=used.get(row.id, []),
                folder=folders.get(row.folder_id) if row.folder_id else None,
                automation=automation,
                created_at=row.created_at,
                updated_at=row.updated_at,
            )
        )
    return notes


async def list_workspace_notes(
    session: AsyncSession,
    workspace_id: int,
    *,
    query: str | None = None,
    include_documents: bool = True,
) -> list[WorkspaceNoteRead]:
    """Notes matching ``query`` (title or text, any case), newest first.

    ``include_documents`` is off for a member who may read notebooks but not
    the workspace's documents: they see notebook notes only.
    """
    query = (query or "").strip() or None
    notes = await _notebook_notes(session, workspace_id, query)
    if include_documents:
        notes += await _note_documents(session, workspace_id, query)
    notes.sort(key=lambda note: (note.created_at, note.kind, note.id), reverse=True)
    return notes
