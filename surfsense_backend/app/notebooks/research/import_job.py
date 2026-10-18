"""Fill a pending research document: read its URL, store the content, queue indexing.

Always ends with the document ready. When nothing could be read, the title and
the search snippet become the content, so the source is never left broken.
Papers (a result carrying ``paper`` metadata) are read by the academic reader,
which prefers open-access full text; everything else by URL.
"""

from __future__ import annotations

import hashlib
import logging
from collections.abc import Awaitable, Callable, Sequence
from datetime import UTC, datetime
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, DocumentStatus

from .capability_runner import CapabilityRunner
from .ingest.fallback import fallback_markdown
from .ingest.fetch import fetch_content
from .ingest.fetched import FetchedContent

logger = logging.getLogger(__name__)

ContentFetcher = Callable[
    [AsyncSession, CapabilityRunner, str, UUID | str],
    Awaitable[FetchedContent | None],
]
Recorder = Callable[[AsyncSession, Sequence[Document]], Awaitable[object]]
# (session, runner, document metadata, title, user id) -> the paper's content.
PaperFetcher = Callable[
    [AsyncSession, CapabilityRunner, dict, str, UUID | str],
    Awaitable[FetchedContent | None],
]
ReindexQueue = Callable[[int, str], None]


def _queue_reindex(document_id: int, user_id: str) -> None:
    from app.tasks.celery_tasks.document_reindex_tasks import reindex_document_task

    reindex_document_task.delay(document_id, user_id)


async def _record(session: AsyncSession, documents: Sequence[Document]) -> object:
    from app.knowledge_store.service import record_prepared_documents

    return await record_prepared_documents(session, documents)


async def read_paper(
    session: AsyncSession,
    runner: CapabilityRunner,
    metadata: dict,
    title: str,
    user_id: UUID | str,
) -> FetchedContent | None:
    from .academic.ingest import PaperReader

    return await PaperReader(session, runner, user_id).read(metadata, title)


async def fill_document(
    session: AsyncSession,
    document_id: int,
    user_id: UUID | str,
    runner: CapabilityRunner,
    *,
    fetch: ContentFetcher = fetch_content,
    fetch_paper: PaperFetcher = read_paper,
    record: Recorder = _record,
    queue_reindex: ReindexQueue = _queue_reindex,
) -> Document | None:
    document = await session.scalar(select(Document).where(Document.id == document_id))
    if document is None:
        return None
    metadata = dict(document.document_metadata or {})
    url = metadata.get("url") or ""

    document.status = DocumentStatus.processing()
    await session.commit()

    try:
        if metadata.get("paper"):
            content = await fetch_paper(
                session, runner, metadata, document.title, user_id
            )
        else:
            content = await fetch(session, runner, url, user_id) if url else None
    except Exception:
        logger.exception("reading research source %s failed", url)
        content = None

    if content is not None:
        markdown = content.markdown
        metadata.update(content.metadata)
        metadata["fetched"] = True
        if content.title and (document.title.strip() == url or metadata.get("paper")):
            document.title = content.title[:500]
    else:
        markdown = fallback_markdown(document.title, metadata.get("snippet"), url)
        metadata["fetched"] = False

    document.source_markdown = markdown
    document.content = markdown
    document.content_hash = hashlib.sha256(markdown.encode()).hexdigest()
    document.content_needs_reindexing = True
    document.document_metadata = metadata
    document.updated_at = datetime.now(UTC)
    document.status = DocumentStatus.ready()
    await session.commit()

    try:
        await record(session, [document])
    except Exception:
        logger.exception(
            "recording research document %s to the store failed", document_id
        )
    queue_reindex(document.id, str(user_id))
    return document
