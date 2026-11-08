"""Copy a document's identity onto the chunk rows cut from it.

``chunks.workspace_id`` repeats what ``documents.workspace_id`` already says,
so that filtered vector search keeps its filter and its HNSW index on one
table (see ``Chunk.workspace_id``). A chunk whose copy disagrees with its
document is invisible to its own workspace's search and visible to another's,
so the two fields are always written together, here.
"""

from __future__ import annotations

from collections.abc import Iterable

from app.db import Chunk, Document


def stamp_owning_document(chunks: Iterable[Chunk], document: Document) -> None:
    """Point ``chunks`` at ``document`` and at the workspace that owns it."""
    for chunk in chunks:
        chunk.document_id = document.id
        chunk.workspace_id = document.workspace_id


__all__ = ["stamp_owning_document"]
