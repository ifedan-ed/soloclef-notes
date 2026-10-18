"""Stamp pages on chunks of PDFs indexed before chunks carried pages.

Uses only the stored original's text layer: no parser is re-run, nothing is
billed. Idempotent: a document whose chunks all have pages is not selected,
and re-stamping writes only rows whose pages change.
"""

from __future__ import annotations

import logging
from collections.abc import Callable
from dataclasses import dataclass, field

from sqlalchemy import func, or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Chunk, Document
from app.file_storage.persistence.enums import DocumentFileKind
from app.file_storage.persistence.models import DocumentFile
from app.file_storage.service import get_document_file

from .chunk_page_stamper import StampOutcome, stamp_chunk_pages

logger = logging.getLogger(__name__)


@dataclass
class BackfillReport:
    documents: int = 0
    stamped: int = 0
    chunks: int = 0
    placed: int = 0
    changed: int = 0
    failed: int = 0
    skipped: dict[str, int] = field(default_factory=dict)

    def add(self, outcome: StampOutcome) -> None:
        self.documents += 1
        self.chunks += outcome.chunks
        self.placed += outcome.placed
        self.changed += outcome.changed
        if outcome.skipped:
            self.skipped[outcome.skipped] = self.skipped.get(outcome.skipped, 0) + 1
        else:
            self.stamped += 1


def _pdf_original_filter():
    return or_(
        func.lower(DocumentFile.mime_type) == "application/pdf",
        func.lower(DocumentFile.original_filename).like("%.pdf"),
    )


async def documents_needing_pages(
    session: AsyncSession,
    *,
    workspace_id: int | None = None,
    document_id: int | None = None,
    include_stamped: bool = False,
    limit: int | None = None,
) -> list[int]:
    """Ids of documents with a PDF original and at least one chunk without a page."""
    query = (
        select(DocumentFile.document_id)
        .where(DocumentFile.kind == DocumentFileKind.ORIGINAL, _pdf_original_filter())
        .distinct()
        .order_by(DocumentFile.document_id)
    )
    if not include_stamped:
        unstamped = select(Chunk.id).where(
            Chunk.document_id == DocumentFile.document_id,
            Chunk.page_start.is_(None),
        )
        query = query.where(unstamped.exists())
    if workspace_id is not None:
        query = query.join(Document, Document.id == DocumentFile.document_id).where(
            Document.workspace_id == workspace_id
        )
    if document_id is not None:
        query = query.where(DocumentFile.document_id == document_id)
    if limit:
        query = query.limit(limit)
    return list((await session.execute(query)).scalars().all())


async def backfill_chunk_pages(
    session: AsyncSession,
    *,
    apply: bool,
    workspace_id: int | None = None,
    document_id: int | None = None,
    include_stamped: bool = False,
    limit: int | None = None,
    on_document: Callable[[StampOutcome], None] | None = None,
) -> BackfillReport:
    """Stamp every selected document; commit each one only when ``apply``."""
    report = BackfillReport()
    ids = await documents_needing_pages(
        session,
        workspace_id=workspace_id,
        document_id=document_id,
        include_stamped=include_stamped,
        limit=limit,
    )
    for doc_id in ids:
        try:
            original = await get_document_file(
                session, document_id=doc_id, kind=DocumentFileKind.ORIGINAL
            )
            outcome = await stamp_chunk_pages(session, doc_id, original=original)
            if apply:
                await session.commit()
            else:
                await session.rollback()
        except Exception:
            await session.rollback()
            report.failed += 1
            logger.warning("Backfill failed for document %d", doc_id, exc_info=True)
            continue
        report.add(outcome)
        if on_document is not None:
            on_document(outcome)
    return report


__all__ = ["BackfillReport", "backfill_chunk_pages", "documents_needing_pages"]
