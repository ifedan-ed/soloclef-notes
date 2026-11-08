"""Start an import: a pending document per pick, linked to the notebook at once.

The rows appear immediately (the notebook shows them while Drive files
download and parse in the background). The Drive indexer fills the same rows:
they share its identity (document type + Drive file id + workspace), so a file
imported before is reused, and re-synced when it changed in Drive.
"""

from __future__ import annotations

from dataclasses import dataclass
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, DocumentType
from app.indexing_pipeline.document_hashing import compute_identifier_hash
from app.indexing_pipeline.indexing_pipeline_service import (
    IndexingPipelineService,
    PlaceholderInfo,
)
from app.notebooks.persistence import NotebookSource

from .schemas import PickedDriveFile


@dataclass(frozen=True)
class ImportTarget:
    connector_id: int
    workspace_id: int
    user_id: UUID
    notebook_id: int | None


def _identity(file_id: str, workspace_id: int) -> str:
    return compute_identifier_hash(
        DocumentType.GOOGLE_DRIVE_FILE.value, file_id, workspace_id
    )


async def _link(session: AsyncSession, notebook_id: int, document_id: int) -> None:
    await session.execute(
        pg_insert(NotebookSource)
        .values(notebook_id=notebook_id, document_id=document_id, enabled=True)
        .on_conflict_do_update(
            constraint="uq_notebook_sources_document", set_={"enabled": True}
        )
    )


async def start_import(
    session: AsyncSession, picks: list[PickedDriveFile], target: ImportTarget
) -> list[int]:
    """The document id of each pick, in order; the caller commits and dispatches."""
    await IndexingPipelineService(session).create_placeholder_documents(
        [
            PlaceholderInfo(
                title=picked.name,
                document_type=DocumentType.GOOGLE_DRIVE_FILE,
                unique_id=picked.id,
                workspace_id=target.workspace_id,
                connector_id=target.connector_id,
                created_by_id=str(target.user_id),
                metadata={
                    "google_drive_file_id": picked.id,
                    "FILE_NAME": picked.name,
                    "connector_id": target.connector_id,
                    "connector_type": "Google Drive",
                },
            )
            for picked in picks
        ]
    )
    identities = {_identity(p.id, target.workspace_id): p.id for p in picks}
    rows = await session.execute(
        select(Document.unique_identifier_hash, Document.id).where(
            Document.unique_identifier_hash.in_(list(identities))
        )
    )
    by_file = {identities[uid]: doc_id for uid, doc_id in rows.all()}
    document_ids = [by_file[p.id] for p in picks if p.id in by_file]
    if target.notebook_id is not None:
        for document_id in document_ids:
            await _link(session, target.notebook_id, document_id)
    return document_ids
