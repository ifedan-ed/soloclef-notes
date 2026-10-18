"""Move a note document into a notebook, where it becomes a notebook note.

The note keeps its text, dates, origin and automation, and from then on shows
in that notebook's Studio panel. The document it came from is deleted (the
same delete as the Notes API's), so the note is not listed twice, unless:

* a notebook uses that document as a source: the document stays, since
  removing it would take it out of that notebook, and the new note is tied to
  it when the source is in this very notebook (it shows "Added as a source");
* the caller may not delete documents (``keep_document``; Editors by
  default): the note is a copy and the document stays, so moving is never a
  way around "Documents (no delete)".
"""

from __future__ import annotations

from uuid import UUID

from fastapi import HTTPException
from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.persistence.models.automation import Automation
from app.db import Document
from app.knowledge_store import service as knowledge_store
from app.notebooks.access import touch
from app.notebooks.persistence import Notebook, NotebookNote

from .note_documents import automation_id_of, display_title, origin_of
from .source_usage import notebooks_using

_BUSY_STATES = ("pending", "processing")


async def move_into_notebook(
    session: AsyncSession,
    *,
    document: Document,
    notebook: Notebook,
    moved_by: UUID | None,
    keep_document: bool = False,
) -> NotebookNote:
    """Make ``document`` a note of ``notebook`` and commit. Both must be in the
    same workspace; the caller has checked permissions, and passes
    ``keep_document`` when it may not delete documents."""
    state = (document.status or {}).get("state") if document.status else None
    if state in _BUSY_STATES:
        raise HTTPException(
            status_code=409,
            detail="This note is still being processed. Try again in a moment.",
        )
    meta = document.document_metadata or {}
    automation_id = automation_id_of(meta)
    if (
        automation_id is not None
        and await session.get(Automation, automation_id) is None
    ):
        automation_id = None  # deleted since; the origin still says "automation"

    used_by = (
        await notebooks_using(
            session, document.workspace_id, [(document.id, document.folder_id)]
        )
    ).get(document.id, [])
    note = NotebookNote(
        notebook_id=notebook.id,
        title=display_title(document.title)[:300],
        content=document.source_markdown
        if document.source_markdown is not None
        else (document.content or ""),
        created_by_id=document.created_by_id or moved_by,
        origin=origin_of(meta),
        automation_id=automation_id,
        converted_document_id=document.id
        if any(ref.id == notebook.id for ref in used_by)
        else None,
        created_at=document.created_at,
        updated_at=document.updated_at,
    )
    session.add(note)
    touch(notebook)
    if not used_by and not keep_document:
        # Before the row goes: it is what says where the file is.
        await knowledge_store.record_deleted_documents(
            session,
            [document],
            author_user_id=str(moved_by) if moved_by else None,
        )
        await session.delete(document)
    await session.commit()
    await session.refresh(note)
    return note
