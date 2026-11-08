"""Integration tests for optimized DocumentHybridSearchRetriever.

Verifies the character allowance applied along reading order, and column
pruning.
"""

import pytest

from app.retriever.documents_hybrid_search import DocumentHybridSearchRetriever

from .conftest import DUMMY_EMBEDDING

pytestmark = pytest.mark.integration


async def test_a_document_is_bounded_by_its_character_allowance(
    db_session, seed_large_doc, monkeypatch
):
    """The 35-chunk document contributes text, not a chunk count.

    Its chunks are unranked -- the document matched as a whole -- so the
    allowance is spent along reading order and stops there.
    """
    from app.config import config

    monkeypatch.setattr(config, "RETRIEVAL_CONTEXT_BUDGET_CHARS", 10_000)
    monkeypatch.setattr(config, "RETRIEVAL_DOCUMENT_CONTEXT_BUDGET_CHARS", 200)

    retriever = DocumentHybridSearchRetriever(db_session)
    results = await retriever.hybrid_search(
        query_text="quarterly performance review",
        top_k=10,
        workspace_id=seed_large_doc["workspace"].id,
        query_embedding=DUMMY_EMBEDDING,
    )

    large_doc_id = seed_large_doc["large_doc"].id
    for result in results:
        if result["document"].get("id") == large_doc_id:
            carried = sum(len(c["content"]) for c in result["chunks"])
            assert 0 < carried <= 200
            assert len(result["chunks"]) < 35
            break
    else:
        pytest.fail("Large doc not found in search results")


async def test_doc_metadata_populated(db_session, seed_large_doc):
    """Document metadata should be present from the RRF results."""
    space_id = seed_large_doc["workspace"].id

    retriever = DocumentHybridSearchRetriever(db_session)
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


async def test_chunks_ordered_by_id(db_session, seed_large_doc):
    """Chunks within each document should be ordered by chunk ID."""
    space_id = seed_large_doc["workspace"].id

    retriever = DocumentHybridSearchRetriever(db_session)
    results = await retriever.hybrid_search(
        query_text="quarterly performance review",
        top_k=10,
        workspace_id=space_id,
        query_embedding=DUMMY_EMBEDDING,
    )

    for result in results:
        chunk_ids = [c["chunk_id"] for c in result["chunks"]]
        assert chunk_ids == sorted(chunk_ids), "Chunks not ordered by ID"
