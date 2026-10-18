"""Every search embeds its query with the question model; everything stored
is embedded with the document model.

The call sites, by kind: chat retrieval (``search_chunks``), the document and
chunk retrievers, the connector service's searches and notebook search embed
a question; the indexing pipeline, the older file processors (via
``create_document_chunks``), notes the chat writes, reverts and Drive syncs
embed through ``embed_texts``/``embed_text``. The startup warmup warms the
question model. Each is called for real here, with two recording clients in
place of the configured ones, and the test reads which client got the text.
"""

from __future__ import annotations

import contextlib
from types import SimpleNamespace

import pytest

from app.agents.chat.multi_agent_chat.shared.retrieval.hybrid_search import (
    search_chunks,
)
from app.agents.chat.multi_agent_chat.shared.retrieval.models import SearchScope
from app.retriever.chunks_hybrid_search import ChucksHybridSearchRetriever
from app.retriever.documents_hybrid_search import DocumentHybridSearchRetriever

pytestmark = pytest.mark.unit

QUESTION = "what did the flood destroy?"


async def test_chat_retrieval_embeds_the_question_with_the_question_model(
    roles, no_rows_session
):
    await search_chunks(
        no_rows_session, workspace_id=1, query=QUESTION, scope=SearchScope(), top_k=3
    )

    assert roles.query.texts == [QUESTION]
    assert roles.document.requests == []


@pytest.mark.parametrize(
    "retriever_class", [DocumentHybridSearchRetriever, ChucksHybridSearchRetriever]
)
@pytest.mark.parametrize("mode", ["vector_search", "hybrid_search"])
async def test_the_retrievers_embed_the_question_with_the_question_model(
    roles, no_rows_session, retriever_class, mode
):
    retriever = retriever_class(no_rows_session)

    await getattr(retriever, mode)(query_text=QUESTION, top_k=3, workspace_id=1)

    assert roles.query.texts == [QUESTION]
    assert roles.document.requests == []


async def test_the_connector_search_embeds_the_question_once_with_the_question_model(
    roles, no_rows_session, monkeypatch
):
    from contextlib import asynccontextmanager

    from app.services import connector_service

    @asynccontextmanager
    async def no_rows_sessions():
        yield no_rows_session

    monkeypatch.setattr(connector_service, "async_session_maker", no_rows_sessions)
    service = connector_service.ConnectorService(no_rows_session, workspace_id=1)

    await service.search_files(user_query=QUESTION, workspace_id=1, top_k=3)

    assert roles.query.texts == [QUESTION]
    assert roles.document.requests == []


async def test_notebook_search_embeds_the_question_with_the_question_model(
    roles, no_rows_session
):
    from app.notebooks.search.service import search_notebooks

    await search_notebooks(no_rows_session, 1, QUESTION, reranker=None)

    assert roles.query.texts == [QUESTION]
    assert roles.document.requests == []


async def test_the_startup_warmup_warms_the_question_model(roles):
    from app.app import _warm_embedding_model

    await _warm_embedding_model()

    assert roles.query.texts == ["warmup"]
    assert roles.document.requests == []


def test_stored_texts_are_embedded_with_the_document_model(roles):
    from app.utils.document_converters import embed_text, embed_texts

    embed_texts(["a chunk", "another chunk"])
    embed_text("a note")

    assert roles.document.texts == ["a chunk", "another chunk", "a note"]
    assert roles.query.requests == []


async def test_the_indexing_pipeline_embeds_with_the_document_model(roles, monkeypatch):
    from app.config import config
    from app.indexing_pipeline.cache.cached_indexing import build_chunk_embeddings

    monkeypatch.setattr(config, "EMBEDDING_CACHE_ENABLED", False)
    markdown = "# A document\n\nIts first paragraph, about the flood."

    summary, chunks = await build_chunk_embeddings(markdown, use_code_chunker=False)

    assert roles.document.texts[0] == markdown
    assert len(roles.document.texts) == 1 + len(chunks)
    assert summary is not None
    assert roles.query.requests == []


async def test_the_older_file_processors_embed_chunks_with_the_document_model(roles):
    from app.utils.document_converters import create_document_chunks

    chunks = await create_document_chunks("A short document about rain.")

    assert [c.content for c in chunks] == roles.document.texts
    assert roles.query.requests == []


# --- Every writer of stored vectors, called for real with a session that
# finds nothing and accepts everything: each reaches the document model only.


class _Result:
    def __init__(self, value=None) -> None:
        self.value = value

    def scalar_one_or_none(self):
        return self.value

    def scalar(self):
        return self.value

    def first(self):
        return self.value

    def scalars(self):
        return self

    def all(self):
        return []


class _WriterSession:
    """An AsyncSession stand-in: ``found`` answers successive queries, ``rows``
    answers ``get``; whatever is added is kept in ``added``."""

    def __init__(self, *, found=(), rows=None) -> None:
        self.found = list(found)
        self.rows = rows or {}
        self.added: list = []
        self.no_autoflush = contextlib.nullcontext()

    async def execute(self, *_args, **_kwargs):
        return _Result(self.found.pop(0) if self.found else None)

    async def get(self, _model, key):
        return self.rows.get(key)

    def add(self, obj) -> None:
        self.added.append(obj)

    def add_all(self, objs) -> None:
        self.added.extend(objs)

    async def flush(self) -> None:
        pass

    async def commit(self) -> None:
        pass

    async def rollback(self) -> None:
        pass

    async def refresh(self, _obj) -> None:
        pass

    async def delete(self, _obj) -> None:
        pass


class _TaskLog:
    def __init__(self, *_args, **_kwargs) -> None:
        pass

    async def log_task_start(self, **_kwargs):
        return SimpleNamespace(id=1)

    async def log_task_success(self, *_args, **_kwargs):
        return None

    async def log_task_failure(self, *_args, **_kwargs):
        return None


async def _nothing(*_args, **_kwargs):
    return None


def _only_the_document_model(roles, *expected: str) -> None:
    for text in expected:
        assert any(text in t for t in roles.document.texts), text
    assert roles.query.requests == []


NOTE = "Rain fell all night and the river carried away the footbridge."


async def test_a_note_the_chat_writes_is_embedded_with_the_document_model(
    roles, monkeypatch
):
    from app.agents.chat.multi_agent_chat.main_agent.middleware.kb_persistence import (
        middleware as kb_persistence,
    )

    monkeypatch.setattr(kb_persistence, "ensure_folder_hierarchy", _nothing)

    await kb_persistence._create_document(
        _WriterSession(),
        virtual_path="/documents/notes/flood.md",
        content=NOTE,
        workspace_id=1,
        created_by_id=None,
    )

    _only_the_document_model(roles, NOTE)


async def test_a_note_the_chat_edits_is_embedded_with_the_document_model(
    roles, monkeypatch
):
    from app.agents.chat.multi_agent_chat.main_agent.middleware.kb_persistence import (
        middleware as kb_persistence,
    )
    from app.db import Document, DocumentType

    existing = Document(
        id=7, workspace_id=1, document_type=DocumentType.NOTE, document_metadata={}
    )

    updated = await kb_persistence._update_document(
        _WriterSession(found=[existing]),
        doc_id=7,
        content=NOTE,
        virtual_path="/documents/notes/flood.md",
        workspace_id=1,
    )

    assert updated is existing
    _only_the_document_model(roles, NOTE)


def _revision(**changes):
    values = {
        "document_id": 7,
        "workspace_id": 1,
        "title_before": "flood.md",
        "content_before": NOTE,
        "folder_id_before": None,
        "metadata_before": {"virtual_path": "/documents/flood.md"},
        "chunks_before": [{"content": "the footbridge was carried away"}],
    }
    values.update(changes)
    return SimpleNamespace(**values)


async def test_a_reverted_edit_is_embedded_with_the_document_model(roles):
    from app.services import revert_service

    doc = SimpleNamespace(id=7, workspace_id=1, document_metadata={})

    outcome = await revert_service._restore_in_place_document(
        _WriterSession(rows={7: doc}), revision=_revision()
    )

    assert outcome.status == "ok"
    _only_the_document_model(roles, NOTE, "the footbridge was carried away")


async def test_a_reverted_delete_is_embedded_with_the_document_model(roles):
    from app.services import revert_service

    outcome = await revert_service._reinsert_document_from_revision(
        _WriterSession(), revision=_revision(document_id=None)
    )

    assert outcome.status == "ok"
    _only_the_document_model(roles, NOTE, "the footbridge was carried away")


async def test_a_drive_sync_is_embedded_with_the_document_model(roles, monkeypatch):
    from app.services.google_drive.kb_sync_service import GoogleDriveKBSyncService
    from app.tasks.connector_indexers import base

    monkeypatch.setattr(base, "check_document_by_unique_identifier", _nothing)
    monkeypatch.setattr(base, "check_duplicate_document_by_hash", _nothing)
    monkeypatch.setattr(base, "safe_set_chunks", _nothing)

    result = await GoogleDriveKBSyncService(_WriterSession()).sync_after_create(
        file_id="drive-1",
        file_name="flood.md",
        mime_type="text/markdown",
        web_view_link=None,
        content=NOTE,
        connector_id=3,
        workspace_id=1,
        user_id="user-1",
    )

    assert result == {"status": "success"}
    _only_the_document_model(roles, f"Google Drive File: flood.md\n\n{NOTE}", NOTE)


async def test_an_uploaded_file_is_embedded_with_the_document_model(roles, monkeypatch):
    from app.tasks.document_processors import _save

    monkeypatch.setattr(_save, "find_existing_document_with_migration", _nothing)

    document = await _save.save_file_document(
        _WriterSession(), "flood.md", NOTE, 1, "user-1", "DOCLING"
    )

    assert document.content == f"File: flood.md\n\n{NOTE}"
    _only_the_document_model(roles, f"File: flood.md\n\n{NOTE}", NOTE)


async def test_a_markdown_file_is_embedded_with_the_document_model(roles, monkeypatch):
    from app.tasks.document_processors import markdown_processor

    monkeypatch.setattr(markdown_processor, "TaskLoggingService", _TaskLog)
    monkeypatch.setattr(
        markdown_processor, "find_existing_document_with_migration", _nothing
    )

    document = await markdown_processor.add_received_markdown_file_document(
        _WriterSession(), "flood.md", NOTE, 1, "user-1"
    )

    assert document is not None
    _only_the_document_model(roles, f"File: flood.md\n\n{NOTE}", NOTE)


async def test_a_page_from_the_extension_is_embedded_with_the_document_model(
    roles, monkeypatch
):
    from app.knowledge_store import service as knowledge_store
    from app.schemas import ExtensionDocumentMetadata
    from app.tasks.document_processors import extension_processor

    monkeypatch.setattr(extension_processor, "TaskLoggingService", _TaskLog)
    monkeypatch.setattr(
        extension_processor, "check_document_by_unique_identifier", _nothing
    )
    monkeypatch.setattr(knowledge_store, "record_prepared_documents", _nothing)
    page = SimpleNamespace(
        metadata=ExtensionDocumentMetadata(
            BrowsingSessionId="s",
            VisitedWebPageURL="https://example.org/flood",
            VisitedWebPageTitle="The flood",
            VisitedWebPageDateWithTimeInISOString="2026-10-12T00:00:00Z",
            VisitedWebPageReffererURL="",
            VisitedWebPageVisitDurationInMilliseconds="1000",
        ),
        page_content=NOTE,
    )

    document = await extension_processor.add_extension_received_document(
        _WriterSession(), page, 1, "user-1"
    )

    assert document is not None
    _only_the_document_model(roles, "https://example.org/flood", NOTE)
