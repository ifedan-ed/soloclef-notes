"""Integration tests for optimized ChucksHybridSearchRetriever.

Verifies the character allowance one search spends, column pruning,
and doc metadata caching from RRF results.
"""

import pytest

from app.retriever.chunks_hybrid_search import ChucksHybridSearchRetriever

from .conftest import DUMMY_EMBEDDING

pytestmark = pytest.mark.integration


@pytest.fixture
def small_document_allowance(monkeypatch):
    """Room for a few of the seeded chunks, which are about 50 characters."""
    from app.config import config

    monkeypatch.setattr(config, "RETRIEVAL_CONTEXT_BUDGET_CHARS", 10_000)
    monkeypatch.setattr(config, "RETRIEVAL_DOCUMENT_CONTEXT_BUDGET_CHARS", 200)
    return 200


async def test_a_document_is_bounded_by_its_character_allowance(
    db_session, seed_large_doc, small_document_allowance
):
    """The 35-chunk document contributes text, not a chunk count."""
    space_id = seed_large_doc["workspace"].id

    retriever = ChucksHybridSearchRetriever(db_session)
    results = await retriever.hybrid_search(
        query_text="quarterly performance review",
        top_k=10,
        workspace_id=space_id,
        query_embedding=DUMMY_EMBEDDING,
    )

    large_doc_id = seed_large_doc["large_doc"].id
    for result in results:
        if result["document"].get("id") == large_doc_id:
            carried = sum(len(c["content"]) for c in result["chunks"])
            assert 0 < carried <= small_document_allowance
            assert len(result["chunks"]) < 35
            break
    else:
        pytest.fail("Large doc not found in search results")


async def test_the_whole_search_is_bounded_by_its_character_allowance(
    db_session, seed_large_doc, monkeypatch
):
    from app.config import config

    monkeypatch.setattr(config, "RETRIEVAL_CONTEXT_BUDGET_CHARS", 150)
    monkeypatch.setattr(config, "RETRIEVAL_DOCUMENT_CONTEXT_BUDGET_CHARS", 150)

    retriever = ChucksHybridSearchRetriever(db_session)
    results = await retriever.hybrid_search(
        query_text="quarterly performance review",
        top_k=10,
        workspace_id=seed_large_doc["workspace"].id,
        query_embedding=DUMMY_EMBEDDING,
    )

    carried = sum(
        len(chunk["content"]) for result in results for chunk in result["chunks"]
    )
    assert 0 < carried <= 150


async def test_doc_metadata_populated_from_rrf(db_session, seed_large_doc):
    """Document metadata (title, type, etc.) should be present even without joinedload."""
    space_id = seed_large_doc["workspace"].id

    retriever = ChucksHybridSearchRetriever(db_session)
    results = await retriever.hybrid_search(
        query_text="quarterly performance review",
        top_k=10,
        workspace_id=space_id,
        query_embedding=DUMMY_EMBEDDING,
    )

    assert len(results) >= 1
    for result in results:
        doc = result["document"]
        assert "id" in doc
        assert "title" in doc
        assert doc["title"]
        assert "document_type" in doc
        assert doc["document_type"] is not None


async def test_matched_chunk_ids_tracked(db_session, seed_large_doc):
    """matched_chunk_ids should contain the chunk IDs that appeared in the RRF results."""
    space_id = seed_large_doc["workspace"].id

    retriever = ChucksHybridSearchRetriever(db_session)
    results = await retriever.hybrid_search(
        query_text="quarterly performance review",
        top_k=10,
        workspace_id=space_id,
        query_embedding=DUMMY_EMBEDDING,
    )

    for result in results:
        matched = result.get("matched_chunk_ids", [])
        chunk_ids_in_result = {c["chunk_id"] for c in result["chunks"]}
        for mid in matched:
            assert mid in chunk_ids_in_result, (
                f"matched_chunk_id {mid} not found in chunks"
            )


async def test_chunks_ordered_by_id(db_session, seed_large_doc):
    """Chunks within each document should be ordered by chunk ID (original order)."""
    space_id = seed_large_doc["workspace"].id

    retriever = ChucksHybridSearchRetriever(db_session)
    results = await retriever.hybrid_search(
        query_text="quarterly performance review",
        top_k=10,
        workspace_id=space_id,
        query_embedding=DUMMY_EMBEDDING,
    )

    for result in results:
        chunk_ids = [c["chunk_id"] for c in result["chunks"]]
        assert chunk_ids == sorted(chunk_ids), "Chunks not ordered by ID"


async def test_score_is_positive_float(db_session, seed_large_doc):
    """Each result should have a positive float score from RRF."""
    space_id = seed_large_doc["workspace"].id

    retriever = ChucksHybridSearchRetriever(db_session)
    results = await retriever.hybrid_search(
        query_text="quarterly performance review",
        top_k=10,
        workspace_id=space_id,
        query_embedding=DUMMY_EMBEDDING,
    )

    assert len(results) >= 1
    for result in results:
        assert isinstance(result["score"], float)
        assert result["score"] > 0


async def test_top_k_counts_documents_not_chunks(db_session, seed_large_doc):
    """``top_k`` is documented as a document count, so a chunk-dense document
    must not be able to consume the whole result set.

    ``large_doc`` has 35 matching chunks and ``small_doc`` has one. If the
    fused query is capped at ``top_k`` *chunk* rows, the large document fills
    that cap by itself and the second document never reaches the caller.
    """
    space_id = seed_large_doc["workspace"].id

    retriever = ChucksHybridSearchRetriever(db_session)
    results = await retriever.hybrid_search(
        query_text="quarterly performance review",
        top_k=10,
        workspace_id=space_id,
        query_embedding=DUMMY_EMBEDDING,
    )

    returned = {result["document"]["id"] for result in results}
    assert seed_large_doc["large_doc"].id in returned
    assert seed_large_doc["small_doc"].id in returned


async def test_top_k_still_caps_the_number_of_documents(db_session, seed_large_doc):
    """The cap itself keeps working: two matching documents, ``top_k=1``."""
    space_id = seed_large_doc["workspace"].id

    retriever = ChucksHybridSearchRetriever(db_session)
    results = await retriever.hybrid_search(
        query_text="quarterly performance review",
        top_k=1,
        workspace_id=space_id,
        query_embedding=DUMMY_EMBEDDING,
    )

    assert len(results) == 1
