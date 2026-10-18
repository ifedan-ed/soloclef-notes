"""Turn a notebook note into a workspace document and link it as a source.

Goes through the same path the Notes feature uses to create a NOTE document
(knowledge-store record, then the editor's reindex task), so the converted
note is searchable, citable and editable like any other note.
"""

from __future__ import annotations

import hashlib
from datetime import UTC, datetime
from uuid import UUID

from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, DocumentType
from app.knowledge_store.service import record_prepared_documents

from .persistence import Notebook, NotebookNote, NotebookSource


def _queue_reindex(document_id: int, user_id: str) -> None:
    from app.tasks.celery_tasks.document_reindex_tasks import reindex_document_task

    reindex_document_task.delay(document_id, user_id)


async def convert_note_to_source(
    session: AsyncSession,
    *,
    notebook: Notebook,
    note: NotebookNote,
    user_id: UUID,
    queue_reindex=None,
) -> Document:
    title = note.title.strip() or "Untitled note"
    markdown = note.content or ""
    document = Document(
        workspace_id=notebook.workspace_id,
        title=title,
        document_type=DocumentType.NOTE,
        content="",
        content_hash=hashlib.sha256(f"{title}\n{markdown}".encode()).hexdigest(),
        source_markdown=markdown,
        content_needs_reindexing=bool(markdown.strip()),
        document_metadata={"NOTE": True, "notebook_id": notebook.id},
        embedding=None,
        updated_at=datetime.now(UTC),
        created_by_id=user_id,
    )
    session.add(document)
    await session.flush()

    await session.execute(
        pg_insert(NotebookSource)
        .values(notebook_id=notebook.id, document_id=document.id)
        .on_conflict_do_nothing()
    )
    note.converted_document_id = document.id
    await session.commit()
    await session.refresh(document)

    await record_prepared_documents(session, [document])
    if markdown.strip():
        (queue_reindex or _queue_reindex)(document.id, str(user_id))
    return document
