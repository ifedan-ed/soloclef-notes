"""What chunk search hands the prompt: the best-ranked text, bounded in characters.

The per-document cap used to be a chunk count applied to chunks in reading
order, so a document contributing more matched chunks than the cap kept the
positionally-first ones and could drop the very chunk that answered the query.
These tests drive ``hybrid_search`` over a stand-in session and assert on the
chunks it returns: ranked selection, character allowances, reading order.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest
from sqlalchemy.sql.elements import TextClause

from app.retriever.chunks_hybrid_search import ChucksHybridSearchRetriever

pytestmark = pytest.mark.unit

WORKSPACE_ID = 7
CHUNK_CHARS = 1_000


class _FusedResult:
    def __init__(self, rows):
        self._rows = rows

    def all(self):
        return self._rows

    def scalars(self):
        return self

    def unique(self):
        return self


class _FusedSession:
    """An AsyncSession stand-in whose only data query returns fused rows."""

    def __init__(self, rows):
        self._rows = rows

    async def execute(self, statement, *args, **kwargs):
        if isinstance(statement, TextClause):
            return _FusedResult([])
        return _FusedResult(self._rows)


def _chunk(chunk_id: int, document_id: int, position: int):
    document = SimpleNamespace(
        id=document_id,
        title=f"document {document_id}",
        document_type=SimpleNamespace(value="FILE"),
        document_metadata={},
    )
    return SimpleNamespace(
        id=chunk_id,
        content="x" * CHUNK_CHARS,
        position=position,
        document=document,
    )


@pytest.fixture
def stub_embedding(monkeypatch):
    from app.config import config

    class _Model:
        dimension = config.EMBEDDING_DIMENSION

        def embed(self, _text):
            return [0.0] * self.dimension

    monkeypatch.setattr(config, "query_embedding_model_instance", _Model())


@pytest.fixture
def small_budget(monkeypatch):
    """Three chunks' worth for the search, two for any one document."""
    from app.config import config

    monkeypatch.setattr(config, "RETRIEVAL_CONTEXT_BUDGET_CHARS", 3 * CHUNK_CHARS)
    monkeypatch.setattr(
        config, "RETRIEVAL_DOCUMENT_CONTEXT_BUDGET_CHARS", 2 * CHUNK_CHARS
    )


async def _search(rows, *, top_k: int = 5) -> list[dict]:
    retriever = ChucksHybridSearchRetriever(_FusedSession(rows))
    return await retriever.hybrid_search(
        query_text="anything",
        top_k=top_k,
        workspace_id=WORKSPACE_ID,
        query_embedding=[0.0] * 8,
    )


async def test_the_best_ranked_chunk_survives_a_document_that_cannot_fit(
    stub_embedding, small_budget
):
    """The defect's shape: the answering chunk is the document's last one.

    Fused rank, not position, decides what is kept, so the chunk that answered
    the query is in the prompt even though the document is cut back.
    """
    best = _chunk(chunk_id=96, document_id=1, position=5)
    rows = [(best, 0.9)] + [
        (_chunk(chunk_id=90 + position, document_id=1, position=position), 0.5)
        for position in range(5)
    ]

    results = await _search(rows)

    kept = [chunk["chunk_id"] for chunk in results[0]["chunks"]]
    assert 96 in kept, (
        f"the best-ranked chunk was dropped for positionally earlier ones; kept {kept}"
    )


async def test_chunks_come_back_in_reading_order(stub_embedding, small_budget):
    best = _chunk(chunk_id=96, document_id=1, position=5)
    rows = [(best, 0.9), (_chunk(chunk_id=91, document_id=1, position=1), 0.5)]

    results = await _search(rows)

    assert [chunk["chunk_id"] for chunk in results[0]["chunks"]] == [91, 96]


async def test_one_document_cannot_spend_the_whole_search_allowance(
    stub_embedding, small_budget
):
    rows = [
        (_chunk(chunk_id=90 + position, document_id=1, position=position), 0.9)
        for position in range(4)
    ] + [(_chunk(chunk_id=200, document_id=2, position=0), 0.1)]

    results = await _search(rows)

    by_document = {result["document"]["id"]: result for result in results}
    assert len(by_document[1]["chunks"]) == 2
    assert [chunk["chunk_id"] for chunk in by_document[2]["chunks"]] == [200]


async def test_a_document_left_with_no_budget_is_not_returned(
    stub_embedding, small_budget
):
    """A source with no text in the prompt is not a source the prompt can cite."""
    rows = (
        [
            (_chunk(chunk_id=90 + position, document_id=1, position=position), 0.9)
            for position in range(2)
        ]
        + [
            (_chunk(chunk_id=190 + position, document_id=2, position=position), 0.5)
            for position in range(2)
        ]
        + [(_chunk(chunk_id=300, document_id=3, position=0), 0.1)]
    )

    results = await _search(rows)

    assert [result["document"]["id"] for result in results] == [1, 2]


async def test_the_matched_chunk_ids_are_the_chunks_in_the_prompt(
    stub_embedding, small_budget
):
    rows = [(_chunk(chunk_id=96, document_id=1, position=5), 0.9)]

    results = await _search(rows)

    assert results[0]["matched_chunk_ids"] == [96]
    assert results[0]["content"] == "x" * CHUNK_CHARS


async def test_no_fused_chunks_means_no_results(stub_embedding, small_budget):
    assert await _search([]) == []
