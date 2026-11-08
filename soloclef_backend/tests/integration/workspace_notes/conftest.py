"""Fixtures for the Notes page: owners with workspaces, notebooks, note documents."""

from __future__ import annotations

import uuid
from datetime import UTC, datetime

import pytest

from app.db import Document, DocumentType
from tests.integration.notebooks.conftest import (  # noqa: F401 (fixtures)
    make_document,
    make_folder,
    make_notebook,
    make_owner,
)


@pytest.fixture
def make_note_document(db_session):
    """A NOTE document as the assistant writes one into ``/documents``."""

    async def build(
        workspace_id: int,
        title: str = "Summary.md",
        text: str = "# Summary\n\nAll quiet.",
        *,
        created_by_id=None,
        created_at: datetime | None = None,
        folder_id: int | None = None,
        metadata: dict | None = None,
    ) -> Document:
        when = created_at or datetime.now(UTC)
        document = Document(
            workspace_id=workspace_id,
            title=title,
            document_type=DocumentType.NOTE,
            content=text,
            source_markdown=text,
            content_hash=uuid.uuid4().hex,
            unique_identifier_hash=uuid.uuid4().hex,
            document_metadata={"virtual_path": f"/documents/notes/{title}"}
            if metadata is None
            else metadata,
            folder_id=folder_id,
            created_by_id=created_by_id,
            created_at=when,
            updated_at=when,
            status={"state": "ready"},
        )
        db_session.add(document)
        await db_session.flush()
        return document

    return build
