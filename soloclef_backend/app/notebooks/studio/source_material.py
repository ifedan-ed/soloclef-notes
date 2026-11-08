"""Resolve the sources chosen for a generation and compile their text.

The chosen ids must be this notebook's source rows, and the documents they
reach must live in this workspace; anything else is refused as a whole, so a
request can never pull another workspace's content into a generation.
"""

from __future__ import annotations

from dataclasses import dataclass

from fastapi import HTTPException
from sqlalchemy import or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document
from app.deliverables.video.source_budget import (
    VIDEO_SOURCE_MAX_CHARS,
    FittedSource,
    fit_source,
)

from ..persistence import Notebook, NotebookSource

_SEPARATOR = "\n\n"


@dataclass(frozen=True)
class SourceDocument:
    id: int
    title: str
    text: str


async def resolve_source_documents(
    session: AsyncSession, notebook: Notebook, source_ids: list[int] | None
) -> list[SourceDocument]:
    """The documents a generation uses.

    ``source_ids`` are the notebook's source rows (a folder source expands to
    the documents in it); ``None`` means the enabled ones. A source id that is
    not this notebook's refuses the whole request.
    """
    query = select(NotebookSource).where(NotebookSource.notebook_id == notebook.id)
    if source_ids is None:
        query = query.where(NotebookSource.enabled.is_(True))
    else:
        query = query.where(NotebookSource.id.in_(source_ids))
    sources = (await session.scalars(query)).all()
    if source_ids is not None and len(sources) != len(set(source_ids)):
        raise HTTPException(status_code=404, detail="Source not found")

    document_ids = [s.document_id for s in sources if s.document_id is not None]
    folder_ids = [s.folder_id for s in sources if s.folder_id is not None]
    documents = []
    if document_ids or folder_ids:
        documents = (
            await session.scalars(
                select(Document)
                .where(
                    Document.workspace_id == notebook.workspace_id,
                    or_(
                        Document.id.in_(document_ids),
                        Document.folder_id.in_(folder_ids),
                    ),
                )
                .order_by(Document.id)
            )
        ).all()
    if not documents:
        raise HTTPException(
            status_code=400, detail="Choose at least one source to generate from"
        )
    return [
        SourceDocument(
            id=document.id,
            title=document.title,
            text=(document.source_markdown or document.content or "").strip(),
        )
        for document in documents
    ]


def compile_material(documents: list[SourceDocument], budget_chars: int) -> str:
    """Every source, each fitted to a fair share of the budget.

    Short sources keep their full text and hand their unused share to the
    longer ones, so one long document cannot crowd the others out.
    """
    if not documents:
        return ""
    headers = [f"## Source: {document.title}\n\n" for document in documents]
    available = max(budget_chars - sum(len(h) for h in headers), 0)
    remaining = sorted(range(len(documents)), key=lambda i: len(documents[i].text))
    shares: dict[int, int] = {}
    for position, index in enumerate(remaining):
        fair = available // (len(remaining) - position)
        take = min(len(documents[index].text), fair)
        shares[index] = take
        available -= take
    parts = []
    for index, document in enumerate(documents):
        # Condensed by section, not cut: a long paper keeps its later sections.
        text = fit_source(document.text, shares[index]).text
        parts.append(headers[index] + text)
    return _SEPARATOR.join(parts)


def whole_source_material(documents: list[SourceDocument]) -> FittedSource:
    """Every source in full, condensed by section only past the ceiling.

    Video and audio take this, and fit it again to the model that runs them.

    Line breaks are kept: they mark the headings and sections that the
    executor's condensation for a smaller model relies on.
    """
    full = _SEPARATOR.join(
        f"## Source: {document.title}\n\n{document.text}" for document in documents
    )
    return fit_source(full, VIDEO_SOURCE_MAX_CHARS)
