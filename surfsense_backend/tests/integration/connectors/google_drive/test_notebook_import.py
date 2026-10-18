"""Importing Picker picks: pending rows in the notebook at once, then the real indexer.

The indexer downloads from the stand-in Drive, parses through the app's ETL,
charges the billing meter and fills the same rows; embeddings are faked.
"""

from __future__ import annotations

import pytest
from sqlalchemy import select

from app.db import Document, DocumentStatus, DocumentType
from app.notebooks.persistence import NotebookSource
from app.services.etl_credit_service import EtlCreditService
from app.tasks.connector_indexers.google_drive_indexer import (
    index_google_drive_selected_files,
)

from .conftest import connect

pytestmark = pytest.mark.integration

NOTES = {
    "id": "e2eDriveNotes0001",
    "name": "Drive field notes.md",
    "mime_type": "text/markdown",
}
PLAN = {
    "id": "e2eDrivePlan00002",
    "name": "Research plan.txt",
    "mime_type": "text/plain",
}
FOLDER = {
    "id": "e2eDriveFolder004",
    "name": "A folder",
    "mime_type": "application/vnd.google-apps.folder",
}


async def _connected(api, db_session, google, owner):
    await connect(api(owner.auth), owner, google)
    from app.connectors.google_drive.connect.connection_record import users_connection

    return await users_connection(db_session, owner.user.id, owner.workspace.id)


async def _linked(db_session, notebook_id: int) -> set[int]:
    rows = await db_session.scalars(
        select(NotebookSource.document_id).where(
            NotebookSource.notebook_id == notebook_id
        )
    )
    return set(rows.all())


async def test_picks_become_pending_notebook_sources_and_are_queued(
    api, db_session, google, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    connector = await _connected(api, db_session, google, owner)

    response = await api(owner.auth).post(
        f"/api/v1/connectors/{connector.id}/google-drive/import",
        json={"workspace_id": owner.workspace.id, "files": [NOTES, PLAN, FOLDER, NOTES], "notebook_id": notebook.id},
    )
    assert response.status_code == 200, response.text
    body = response.json()
    assert body["queued"] == 2
    assert body["unsupported"] == ["A folder"]
    assert len(body["document_ids"]) == 2

    documents = (
        await db_session.scalars(
            select(Document).where(Document.id.in_(body["document_ids"]))
        )
    ).all()
    assert {d.title for d in documents} == {"Drive field notes.md", "Research plan.txt"}
    assert all(
        DocumentStatus.is_state(d.status, DocumentStatus.PENDING) for d in documents
    )
    assert all(d.document_type == DocumentType.GOOGLE_DRIVE_FILE for d in documents)
    assert await _linked(db_session, notebook.id) == set(body["document_ids"])

    await db_session.refresh(connector)
    assert connector.config["selected_files"] == [
        {"id": NOTES["id"], "name": NOTES["name"]},
        {"id": PLAN["id"], "name": PLAN["name"]},
    ]
    [(connector_id, workspace_id, user_id, picks)] = api.dispatched
    assert (connector_id, workspace_id, user_id) == (
        connector.id,
        owner.workspace.id,
        owner.user.id,
    )
    assert [p.id for p in picks] == [NOTES["id"], PLAN["id"]]


async def test_only_the_owner_imports_through_a_connection(
    api, db_session, google, make_owner, add_member
):
    owner = await make_owner()
    connector = await _connected(api, db_session, google, owner)
    other = await make_owner()
    await add_member(owner.workspace, other.user)
    response = await api(other.auth).post(
        f"/api/v1/connectors/{connector.id}/google-drive/import",
        json={"workspace_id": owner.workspace.id, "files": [NOTES]},
    )
    assert response.status_code == 404


async def test_a_notebook_of_another_workspace_is_refused(
    api, db_session, google, make_owner, make_notebook
):
    owner = await make_owner()
    stranger = await make_owner()
    foreign = await make_notebook(stranger.workspace.id)
    connector = await _connected(api, db_session, google, owner)
    response = await api(owner.auth).post(
        f"/api/v1/connectors/{connector.id}/google-drive/import",
        json={"workspace_id": owner.workspace.id, "files": [NOTES], "notebook_id": foreign.id},
    )
    assert response.status_code in (403, 404)
    assert api.dispatched == []


@pytest.mark.usefixtures(
    "patched_embed_texts", "patched_chunk_text", "task_sessions_in_test_transaction"
)
async def test_the_indexer_fills_the_rows_from_drive_and_charges_the_meter(
    api, db_session, google, make_owner, make_notebook, monkeypatch
):
    charged: list[tuple] = []

    async def record(self, user_id, pages, multiplier=1, **kwargs):
        charged.append((user_id, pages, kwargs.get("workspace_id")))

    monkeypatch.setattr(EtlCreditService, "charge_credits", record)
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    connector = await _connected(api, db_session, google, owner)
    missing = {
        "id": "doesNotExist00000",
        "name": "gone.md",
        "mime_type": "text/markdown",
    }
    imported = (
        await api(owner.auth).post(
            f"/api/v1/connectors/{connector.id}/google-drive/import",
            json={"workspace_id": owner.workspace.id, "files": [NOTES, PLAN, missing], "notebook_id": notebook.id},
        )
    ).json()

    indexed, _skipped, errors = await index_google_drive_selected_files(
        db_session,
        connector.id,
        owner.workspace.id,
        str(owner.user.id),
        files=[(f["id"], f["name"]) for f in (NOTES, PLAN, missing)],
    )
    assert indexed == 2
    assert any("gone.md" in e for e in errors)

    rows = {
        d.title: d
        for d in (
            await db_session.scalars(
                select(Document).where(Document.id.in_(imported["document_ids"]))
            )
        ).all()
    }
    await db_session.refresh(rows["Drive field notes.md"])
    notes = rows["Drive field notes.md"]
    assert DocumentStatus.is_state(notes.status, DocumentStatus.READY)
    assert "PERIWINKLE-DRIVE" in (notes.source_markdown or notes.content)
    assert DocumentStatus.is_state(
        rows["Research plan.txt"].status, DocumentStatus.READY
    )
    # A pick Drive no longer has does not stay pending.
    assert DocumentStatus.is_state(rows["gone.md"].status, DocumentStatus.FAILED)
    assert await _linked(db_session, notebook.id) == set(imported["document_ids"])
    assert charged and all(ws == owner.workspace.id for _u, _p, ws in charged)
    assert "files/e2eDriveNotes0001?alt=media" in google.snapshot()["drive_requests"]


@pytest.mark.usefixtures(
    "patched_embed_texts", "patched_chunk_text", "task_sessions_in_test_transaction"
)
async def test_a_resync_rereads_only_what_changed_in_drive(
    api, db_session, google, make_owner, monkeypatch
):
    async def record(self, *args, **kwargs):
        return None

    monkeypatch.setattr(EtlCreditService, "charge_credits", record)
    owner = await make_owner()
    connector = await _connected(api, db_session, google, owner)
    picks = [(NOTES["id"], NOTES["name"]), (PLAN["id"], PLAN["name"])]
    await index_google_drive_selected_files(
        db_session, connector.id, owner.workspace.id, str(owner.user.id), files=picks
    )
    with google.lock:
        google.files[
            NOTES["id"]
        ].content = b"# Drive field notes\n\nNow it says SAFFRON.\n"
        google.files[NOTES["id"]].modified = "2026-09-20T12:00:00.000Z"
        google.drive_requests.clear()

    indexed, skipped, _errors = await index_google_drive_selected_files(
        db_session, connector.id, owner.workspace.id, str(owner.user.id), files=picks
    )
    assert (indexed, skipped) == (1, 1)
    downloads = [
        r for r in google.snapshot()["drive_requests"] if r.endswith("alt=media")
    ]
    assert downloads == [f"files/{NOTES['id']}?alt=media"]
