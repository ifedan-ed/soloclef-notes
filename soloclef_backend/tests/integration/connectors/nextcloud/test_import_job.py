"""The background half of an import: download, parse, index, or hand over to a duplicate.

The Nextcloud server and the parser are the boundaries; indexing (chunks,
embeddings) and the database are real.
"""

from __future__ import annotations

import pytest
from sqlalchemy import select

from app.connectors.nextcloud.client import NextcloudAccount
from app.connectors.nextcloud.import_job import Extracted, import_document
from app.connectors.nextcloud.import_selection import ImportPlan, PlannedFile
from app.connectors.nextcloud.import_start import ImportOptions, start_import
from app.connectors.nextcloud.webdav_listing import RemoteEntry
from app.db import Chunk, Document, DocumentStatus, DocumentType
from app.etl_pipeline.etl_document import DocumentParser
from app.notebooks.persistence import NotebookSource

from .conftest import FLUIDS

pytestmark = pytest.mark.integration

ACCOUNT = NextcloudAccount("https://cloud.example.com", "alice", "app-pw")
TEXT = "# Fluids\n\nMaintenance fluids follow the Holliday-Segar rule."


def _entry(etag: str = "f1") -> RemoteEntry:
    return RemoteEntry(
        path=FLUIDS,
        name="fluids.md",
        is_folder=False,
        size=12,
        modified=None,
        content_type="text/markdown",
        etag=etag,
    )


async def _pending(db_session, owner, connector, notebook=None) -> int:
    started = await start_import(
        db_session,
        ImportPlan(
            files=[PlannedFile(entry=_entry(), folder_parts=(), picked_folder=None)]
        ),
        ACCOUNT,
        ImportOptions(
            connector_id=connector.id,
            workspace_id=owner.workspace.id,
            user_id=owner.user.id,
            parser=DocumentParser.LOCAL,
            use_vision_llm=False,
            notebook_id=notebook.id if notebook else None,
        ),
    )
    await db_session.commit()
    return started.queued_ids[0]


def _fakes(server, calls):
    async def extract(session, path, filename, workspace_id, user_id, parser, vision):
        with open(path, encoding="utf-8") as handle:
            calls.append((filename, parser, vision, handle.read()))
        return Extracted(TEXT, "MARKDOWN", 0)

    async def keep(session, document, data, filename, mime):
        calls.append(("stored", filename, data))

    async def charge(session, user_id, pages):
        calls.append(("charged", pages))

    return {
        "client_for": lambda account: server.client(),
        "extract": extract,
        "store_original": keep,
        "charge": charge,
    }


async def test_a_pending_import_becomes_a_ready_indexed_document(
    db_session, server, make_owner, make_connector
):
    owner = await make_owner()
    connector = await make_connector(owner)
    document_id = await _pending(db_session, owner, connector)
    calls = []

    outcome = await import_document(
        db_session,
        document_id,
        str(owner.user.id),
        max_bytes=1024,
        **_fakes(server, calls),
    )

    assert outcome == "ready"
    document = await db_session.get(Document, document_id)
    await db_session.refresh(document)
    assert DocumentStatus.is_state(document.status, DocumentStatus.READY)
    assert document.document_type == DocumentType.FILE
    assert document.source_markdown == TEXT
    assert document.document_metadata["nextcloud"]["etag"] == "f1"
    assert document.document_metadata["ETL_SERVICE"] == "MARKDOWN"
    assert ("fluids.md", "local", False, "# Fluids\n") in calls
    assert ("stored", "fluids.md", b"# Fluids\n") in calls
    chunks = (
        await db_session.scalars(select(Chunk).where(Chunk.document_id == document_id))
    ).all()
    assert chunks


async def test_text_the_workspace_already_has_links_the_existing_document_instead(
    db_session, server, make_owner, make_connector, make_notebook
):
    owner = await make_owner()
    connector = await make_connector(owner)
    notebook = await make_notebook(owner.workspace.id)
    calls = []
    # The same file, uploaded earlier by hand.
    first_id = await _pending(db_session, owner, connector)
    await import_document(
        db_session,
        first_id,
        str(owner.user.id),
        max_bytes=1024,
        **_fakes(server, calls),
    )
    first = await db_session.get(Document, first_id)
    first.unique_identifier_hash = "uploaded-by-hand"
    await db_session.commit()

    placeholder_id = await _pending(db_session, owner, connector, notebook)
    outcome = await import_document(
        db_session,
        placeholder_id,
        str(owner.user.id),
        max_bytes=1024,
        **_fakes(server, calls),
    )

    assert outcome == "duplicate"
    assert await db_session.get(Document, placeholder_id) is None
    links = (
        await db_session.scalars(
            select(NotebookSource.document_id).where(
                NotebookSource.notebook_id == notebook.id
            )
        )
    ).all()
    assert links == [first_id]


async def test_a_file_that_is_gone_fails_the_document_with_a_reason(
    db_session, server, make_owner, make_connector
):
    owner = await make_owner()
    connector = await make_connector(owner)
    document_id = await _pending(db_session, owner, connector)
    server.routes.pop(("GET", "/remote.php/dav/files/alice" + FLUIDS))

    outcome = await import_document(
        db_session,
        document_id,
        str(owner.user.id),
        max_bytes=1024,
        **_fakes(server, []),
    )

    assert outcome == "failed"
    document = await db_session.get(Document, document_id)
    await db_session.refresh(document)
    assert DocumentStatus.is_state(document.status, DocumentStatus.FAILED)
    assert "Not found in Nextcloud" in document.status.get("reason", "")


async def test_a_removed_connection_fails_the_document(
    db_session, server, make_owner, make_connector
):
    owner = await make_owner()
    connector = await make_connector(owner)
    document_id = await _pending(db_session, owner, connector)
    await db_session.delete(connector)
    await db_session.commit()

    outcome = await import_document(
        db_session,
        document_id,
        str(owner.user.id),
        max_bytes=1024,
        **_fakes(server, []),
    )

    assert outcome == "failed"
    document = await db_session.get(Document, document_id)
    await db_session.refresh(document)
    assert "connection was removed" in document.status.get("reason", "")
