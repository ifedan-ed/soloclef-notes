"""Write each chunk's source pages onto its row, for a document whose original is a PDF."""

from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass
from pathlib import PurePosixPath

from sqlalchemy import select, update
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Chunk
from app.file_storage.persistence.enums import DocumentFileKind
from app.file_storage.persistence.models import DocumentFile
from app.file_storage.service import get_document_file, open_document_file_stream

from .page_locator import assign_pages
from .pdf_text_layer import read_pdf_page_texts

logger = logging.getLogger(__name__)


@dataclass(frozen=True, slots=True)
class StampOutcome:
    """What stamping one document did."""

    document_id: int
    chunks: int = 0
    placed: int = 0
    changed: int = 0
    #: Why nothing was stamped: "no_pdf_original", "no_chunks", "no_page_text".
    skipped: str | None = None


def is_pdf(record: DocumentFile) -> bool:
    if (record.mime_type or "").lower() == "application/pdf":
        return True
    return PurePosixPath(record.original_filename or "").suffix.lower() == ".pdf"


async def read_original_bytes(record: DocumentFile) -> bytes:
    parts = [part async for part in open_document_file_stream(record)]
    return b"".join(parts)


async def stamp_chunk_pages(
    session: AsyncSession,
    document_id: int,
    *,
    parser_page_texts: list[str] | None = None,
    original: DocumentFile | None = None,
) -> StampOutcome:
    """Locate every chunk of ``document_id`` in its original PDF and store the pages.

    ``parser_page_texts`` (the parser's own per-page output) is tried first;
    chunks it cannot place are looked up in the PDF's text layer, read from
    the stored original. Only rows whose pages change are written. Does not
    commit.
    """
    if original is None:
        original = await get_document_file(
            session, document_id=document_id, kind=DocumentFileKind.ORIGINAL
        )
    if original is None or not is_pdf(original):
        return StampOutcome(document_id, skipped="no_pdf_original")

    rows = (
        await session.execute(
            select(Chunk.id, Chunk.content, Chunk.page_start, Chunk.page_end)
            .where(Chunk.document_id == document_id)
            .order_by(Chunk.position, Chunk.id)
        )
    ).all()
    if not rows:
        return StampOutcome(document_id, skipped="no_chunks")

    contents = [row.content for row in rows]
    spans = assign_pages(contents, parser_page_texts)
    if any(span is None for span in spans):
        data = await read_original_bytes(original)
        text_layer = await asyncio.to_thread(read_pdf_page_texts, data)
        spans = assign_pages(contents, parser_page_texts, text_layer)

    placed = sum(span is not None for span in spans)
    if not placed:
        return StampOutcome(document_id, chunks=len(rows), skipped="no_page_text")

    updates = []
    for row, span in zip(rows, spans, strict=True):
        start = span.start if span else None
        end = span.end if span else None
        if (row.page_start, row.page_end) != (start, end):
            updates.append({"id": row.id, "page_start": start, "page_end": end})
    if updates:
        await session.execute(update(Chunk), updates)

    return StampOutcome(
        document_id, chunks=len(rows), placed=placed, changed=len(updates)
    )


async def stamp_after_upload(
    session: AsyncSession,
    document_id: int,
    parser_page_texts: list[str] | None,
) -> None:
    """Stamp a freshly indexed upload; never fails the upload.

    Runs in a savepoint, so a failure here rolls back only the stamping.
    """
    try:
        async with session.begin_nested():
            outcome = await stamp_chunk_pages(
                session, document_id, parser_page_texts=parser_page_texts
            )
        await session.commit()
        if outcome.skipped is None:
            logger.info(
                "Stamped pages on %d/%d chunks of document %d",
                outcome.placed,
                outcome.chunks,
                document_id,
            )
    except Exception:
        logger.warning(
            "Could not stamp source pages for document %d", document_id, exc_info=True
        )


__all__ = ["StampOutcome", "is_pdf", "stamp_after_upload", "stamp_chunk_pages"]
