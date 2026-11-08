"""The page reaches whoever reads a chunk or a citation, as an additive field."""

from datetime import UTC, datetime
from types import SimpleNamespace

import pytest

from app.agents.chat.multi_agent_chat.shared.citations import CitationRegistry
from app.agents.chat.multi_agent_chat.shared.document_render import (
    RenderableDocument,
    RenderablePassage,
)
from app.agents.chat.multi_agent_chat.shared.document_render.document import (
    render_document,
)
from app.agents.chat.multi_agent_chat.shared.retrieval.adapter import (
    to_renderable_document,
)
from app.agents.chat.multi_agent_chat.shared.retrieval.models import (
    ChunkHit,
    DocumentHit,
)
from app.schemas.chunks import ChunkRead

pytestmark = pytest.mark.unit


def _chunk(**overrides):
    fields = {
        "id": 42,
        "content": "cited text",
        "document_id": 7,
        "created_at": datetime(2026, 9, 26, tzinfo=UTC),
        "page_start": 12,
        "page_end": 13,
    }
    fields.update(overrides)
    return SimpleNamespace(**fields)


def test_chunk_read_carries_the_page_range():
    payload = ChunkRead.model_validate(_chunk()).model_dump()

    assert payload["page_start"] == 12
    assert payload["page_end"] == 13
    # Additive: the fields that were there are unchanged.
    assert payload["id"] == 42
    assert payload["content"] == "cited text"


def test_chunk_read_page_is_null_when_unknown():
    payload = ChunkRead.model_validate(_chunk(page_start=None, page_end=None))

    assert payload.page_start is None
    assert payload.page_end is None


def test_a_row_from_before_pages_existed_still_validates():
    row = _chunk()
    del row.page_start
    del row.page_end

    assert ChunkRead.model_validate(row).page_start is None


def test_the_registry_display_carries_the_page_but_the_locator_does_not():
    registry = CitationRegistry()
    document = RenderableDocument(
        title="Kawasaki review",
        passages=[
            RenderablePassage(
                content="IVIG within ten days.",
                locator={"document_id": 7, "chunk_id": 42},
                page=12,
            ),
            RenderablePassage(
                content="No page known.",
                locator={"document_id": 7, "chunk_id": 43},
            ),
        ],
    )

    render_document(document, view="excerpt", registry=registry)

    first, second = registry.resolve(1), registry.resolve(2)
    assert first.display["page"] == 12
    assert first.locator == {"document_id": 7, "chunk_id": 42}
    assert "page" not in second.display


def test_search_hits_hand_their_page_to_the_passage():
    hit = DocumentHit(
        document_id=7,
        title="Kawasaki review",
        document_type="FILE",
        metadata={},
        score=1.0,
        chunks=[ChunkHit(chunk_id=42, content="a", position=0, score=1.0, page=12)],
    )

    passage = to_renderable_document(hit).passages[0]

    assert passage.page == 12
