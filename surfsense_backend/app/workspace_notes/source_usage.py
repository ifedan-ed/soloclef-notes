"""Which notebooks use a note document as a source.

A notebook uses a document when it lists the document itself, or a folder
the document sits in at any depth (a source folder reaches everything below
it, as the notebook chat reads it). Both the listing ("Source in ...") and
the move into a notebook (a document still in use stays where it is) ask this.
"""

from __future__ import annotations

from collections.abc import Iterable, Sequence

from sqlalchemy import or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Folder
from app.notebooks.persistence import Notebook, NotebookSource

from .schemas import NotebookRef


def _folder_chain(folder_id: int | None, parents: dict[int, int | None]) -> list[int]:
    """``folder_id`` and every folder above it (a cycle stops the walk)."""
    chain: list[int] = []
    seen: set[int] = set()
    current = folder_id
    while current is not None and current not in seen:
        seen.add(current)
        chain.append(current)
        current = parents.get(current)
    return chain


async def notebooks_using(
    session: AsyncSession,
    workspace_id: int,
    documents: Sequence[tuple[int, int | None]],
) -> dict[int, list[NotebookRef]]:
    """``{document id: notebooks using it}`` for ``(document id, folder id)``
    pairs; documents no notebook uses are left out."""
    if not documents:
        return {}
    parents: dict[int, int | None] = dict(
        (
            await session.execute(
                select(Folder.id, Folder.parent_id).where(
                    Folder.workspace_id == workspace_id
                )
            )
        ).all()
    )
    chains = {
        doc_id: _folder_chain(folder_id, parents) for doc_id, folder_id in documents
    }
    doc_ids = [doc_id for doc_id, _ in documents]
    folder_ids = sorted({f for chain in chains.values() for f in chain})

    conditions = [NotebookSource.document_id.in_(doc_ids)]
    if folder_ids:
        conditions.append(NotebookSource.folder_id.in_(folder_ids))
    rows = (
        await session.execute(
            select(
                NotebookSource.document_id,
                NotebookSource.folder_id,
                Notebook.id,
                Notebook.title,
                Notebook.icon,
            )
            .join(Notebook, Notebook.id == NotebookSource.notebook_id)
            .where(Notebook.workspace_id == workspace_id, or_(*conditions))
            .order_by(Notebook.title, Notebook.id)
        )
    ).all()

    by_document: dict[int, dict[int, NotebookRef]] = {}
    for row in rows:
        ref = NotebookRef(id=row.id, title=row.title, icon=row.icon)
        targets: Iterable[int]
        if row.document_id is not None:
            targets = [row.document_id]
        else:
            targets = [d for d, chain in chains.items() if row.folder_id in chain]
        for doc_id in targets:
            by_document.setdefault(doc_id, {})[ref.id] = ref
    return {doc_id: list(refs.values()) for doc_id, refs in by_document.items()}
