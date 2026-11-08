"""Every chunk a write path persists carries its document's workspace.

``chunks.workspace_id`` is what workspace-scoped vector search filters on, so
a chunk stamped with the wrong workspace -- or none -- is not a slow search,
it is a wrong one: invisible to its own workspace and offered to another. The
column is NOT NULL so a forgotten stamp fails at the INSERT, but these tests
pin the seams that are meant to do the stamping.
"""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import pytest

from app.db import Chunk, Document, DocumentStatus
from app.indexing_pipeline.chunk_ownership import stamp_owning_document
from app.indexing_pipeline.document_persistence import persist_scratch_index

pytestmark = pytest.mark.unit

WORKSPACE_ID = 41


def _document(doc_id: int = 1, workspace_id: int = WORKSPACE_ID) -> Document:
    document = MagicMock(spec=Document)
    document.id = doc_id
    document.workspace_id = workspace_id
    document.content = None
    document.status = DocumentStatus.processing()
    return document


def _chunks(count: int) -> list[Chunk]:
    return [Chunk(content=f"c{i}", embedding=[0.1], position=i) for i in range(count)]


def test_stamp_owning_document_sets_both_owners():
    document = _document(doc_id=9)
    chunks = _chunks(3)

    stamp_owning_document(chunks, document)

    assert [c.document_id for c in chunks] == [9, 9, 9]
    assert [c.workspace_id for c in chunks] == [WORKSPACE_ID] * 3


@pytest.mark.asyncio
async def test_persist_scratch_index_stamps_the_workspace(monkeypatch):
    """The from-scratch indexing path builds chunks before it knows the row id."""
    monkeypatch.setattr(
        "app.indexing_pipeline.document_persistence.set_committed_value",
        lambda *_args, **_kwargs: None,
    )
    session = MagicMock()
    session.commit = AsyncMock()
    chunks = _chunks(5)

    await persist_scratch_index(
        session,
        _document(doc_id=3),
        "body",
        chunks,
        batch_size=2,
        perf=MagicMock(),
    )

    assert {c.workspace_id for c in chunks} == {WORKSPACE_ID}
    assert {c.document_id for c in chunks} == {3}


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "module",
    [
        "app.tasks.document_processors.base",
        "app.tasks.connector_indexers.base",
    ],
)
async def test_safe_set_chunks_stamps_the_workspace(module, monkeypatch):
    """Both re-index helpers replace a document's chunks wholesale."""
    import importlib

    import sqlalchemy.orm.attributes

    # Imported inside the helper, so patching the source module is enough.
    monkeypatch.setattr(
        sqlalchemy.orm.attributes,
        "set_committed_value",
        lambda *_args, **_kwargs: None,
    )
    safe_set_chunks = importlib.import_module(module).safe_set_chunks
    session = MagicMock()
    session.execute = AsyncMock()
    chunks = _chunks(4)

    await safe_set_chunks(session, _document(doc_id=7), chunks)

    assert {c.workspace_id for c in chunks} == {WORKSPACE_ID}
    assert {c.document_id for c in chunks} == {7}


@pytest.mark.asyncio
async def test_create_document_chunks_stamps_the_workspace(monkeypatch):
    """Chunks built here are attached by ``Document(chunks=...)``, which
    cascades the document id but knows nothing about the workspace."""
    from app.utils import document_converters

    monkeypatch.setattr(
        document_converters,
        "embed_texts",
        lambda texts: [[0.1] for _ in texts],
    )

    chunks = await document_converters.create_document_chunks(
        "a paragraph of text to cut up", workspace_id=WORKSPACE_ID
    )

    assert chunks
    assert {c.workspace_id for c in chunks} == {WORKSPACE_ID}
