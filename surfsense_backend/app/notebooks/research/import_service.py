"""Add picked research results to a notebook as workspace documents.

Rows are created at once in a pending state so the sources list shows them
immediately; reading the page and indexing it happens in the background. A URL
the workspace already holds is linked again rather than duplicated, and so is a
paper already imported from another source (same DOI, PMID, PMC or arXiv id).
"""

from __future__ import annotations

import hashlib
from datetime import UTC, datetime
from uuid import UUID

from sqlalchemy import or_, select
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, DocumentStatus
from app.indexing_pipeline.document_hashing import compute_identifier_hash

from ..persistence import Notebook, NotebookSource
from .academic.paper import PaperRef
from .ingest.kind import document_type_for
from .schemas import ImportItem
from .snippets import trim
from .urls import normalize_url


def identity_hash(url: str, workspace_id: int) -> str:
    """The unique identifier a research import of ``url`` carries in this workspace."""
    return compute_identifier_hash(
        document_type_for(url).value, normalize_url(url), workspace_id
    )


_PAPER_IDS = ("doi", "pmid", "pmcid", "arxiv_id")


def _same_paper(paper: PaperRef | None) -> list:
    if paper is None:
        return []
    return [
        Document.document_metadata["paper"][key].as_string() == value
        for key in _PAPER_IDS
        if (value := getattr(paper, key))
    ]


async def find_existing(
    session: AsyncSession, workspace_id: int, url: str, paper: PaperRef | None = None
) -> Document | None:
    """A document in this workspace already holding ``url`` or the same paper."""
    return await session.scalar(
        select(Document)
        .where(
            Document.workspace_id == workspace_id,
            or_(
                Document.unique_identifier_hash == identity_hash(url, workspace_id),
                Document.document_metadata["url"].as_string() == url,
                *_same_paper(paper),
            ),
        )
        .order_by(Document.id)
        .limit(1)
    )


def _unique_by_url(items: list[ImportItem]) -> list[ImportItem]:
    seen: set[str] = set()
    kept = []
    for item in items:
        key = normalize_url(item.url)
        if key not in seen:
            seen.add(key)
            kept.append(item)
    return kept


def _pending_document(item: ImportItem, notebook: Notebook, user_id: UUID) -> Document:
    snippet = trim(item.snippet, 1000) if item.snippet else None
    return Document(
        workspace_id=notebook.workspace_id,
        title=item.title,
        document_type=document_type_for(item.url),
        content=snippet or "",
        content_hash=hashlib.sha256(item.url.encode()).hexdigest(),
        unique_identifier_hash=identity_hash(item.url, notebook.workspace_id),
        document_metadata={
            "url": item.url,
            "snippet": snippet,
            "research_source": item.source,
            "notebook_id": notebook.id,
            **(
                {"paper": item.paper.model_dump(exclude_none=True)}
                if item.paper
                else {}
            ),
        },
        status=DocumentStatus.pending(),
        embedding=None,
        updated_at=datetime.now(UTC),
        created_by_id=user_id,
    )


async def _create_pending(
    session: AsyncSession, item: ImportItem, notebook: Notebook, user_id: UUID
) -> tuple[Document, bool]:
    """Insert the pending row; a concurrent import of the same URL wins instead."""
    document = _pending_document(item, notebook, user_id)
    try:
        async with session.begin_nested():
            session.add(document)
            await session.flush()
    except IntegrityError:
        winner = await find_existing(
            session, notebook.workspace_id, item.url, item.paper
        )
        if winner is None:
            raise
        return winner, False
    return document, True


async def _link_enabled(
    session: AsyncSession, notebook: Notebook, document_id: int
) -> None:
    """Link the document; re-importing one the notebook has switched off turns it back on."""
    link = await session.scalar(
        select(NotebookSource).where(
            NotebookSource.notebook_id == notebook.id,
            NotebookSource.document_id == document_id,
        )
    )
    if link is not None:
        link.enabled = True
        return
    await session.execute(
        pg_insert(NotebookSource)
        .values(notebook_id=notebook.id, document_id=document_id, enabled=True)
        .on_conflict_do_update(
            constraint="uq_notebook_sources_document", set_={"enabled": True}
        )
    )


async def import_items(
    session: AsyncSession,
    *,
    notebook: Notebook,
    items: list[ImportItem],
    user_id: UUID,
) -> list[int]:
    """Link every item to the notebook and return the ids of newly created documents."""
    created: list[int] = []
    for item in _unique_by_url(items):
        document = await find_existing(
            session, notebook.workspace_id, item.url, item.paper
        )
        if document is None:
            document, is_new = await _create_pending(session, item, notebook, user_id)
            if is_new:
                created.append(document.id)
        await _link_enabled(session, notebook, document.id)
    return created
