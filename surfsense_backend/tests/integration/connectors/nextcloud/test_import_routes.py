"""Picking Nextcloud files over HTTP: browse, import, dedupe, notebook links, refresh.

The Nextcloud server (recorded replies) and the Celery dispatch are the
boundaries; the database, permissions and import bookkeeping are real.
"""

from __future__ import annotations

import pytest
from sqlalchemy import select

from app.db import Document, DocumentStatus, Folder
from app.notebooks.persistence import NotebookSource
from tests.unit.connectors.nextcloud.fake_server import DAV

from .conftest import FLUIDS, single_file_reply

pytestmark = pytest.mark.integration


def _url(connector_id: int, tail: str) -> str:
    return f"/api/v1/connectors/{connector_id}/nextcloud/{tail}"


async def _links(db_session, notebook_id):
    rows = (
        await db_session.scalars(
            select(NotebookSource).where(NotebookSource.notebook_id == notebook_id)
        )
    ).all()
    return {
        ("document", r.document_id) if r.document_id else ("folder", r.folder_id)
        for r in rows
    }


async def test_browsing_lists_a_folder_for_the_connectors_owner(
    api, make_owner, make_connector
):
    owner = await make_owner()
    connector = await make_connector(owner)

    response = await api(owner.auth).get(
        _url(connector.id, "browse"), params={"path": "/Open Notebook"}
    )

    assert response.status_code == 200
    body = response.json()
    assert body["path"] == "/Open Notebook"
    assert [(e["name"], e["is_folder"]) for e in body["entries"]] == [
        ("Week 1", True),
        ("Anatomy notes.pdf", False),
    ]


async def test_someone_elses_connection_cannot_be_browsed(
    api, make_owner, make_connector
):
    owner = await make_owner()
    stranger = await make_owner()
    connector = await make_connector(owner)

    response = await api(stranger.auth).get(_url(connector.id, "browse"))

    assert response.status_code == 404


async def test_a_path_that_climbs_out_is_refused(api, make_owner, make_connector):
    owner = await make_owner()
    connector = await make_connector(owner)
    response = await api(owner.auth).get(
        _url(connector.id, "browse"), params={"path": "/a/../../etc"}
    )
    assert response.status_code == 400


async def test_importing_a_file_links_it_to_the_notebook_and_queues_it(
    api, db_session, make_owner, make_connector, make_notebook
):
    owner = await make_owner()
    connector = await make_connector(owner)
    notebook = await make_notebook(owner.workspace.id)

    response = await api(owner.auth).post(
        _url(connector.id, "import"),
        json={
            "workspace_id": owner.workspace.id,
            "items": [{"path": FLUIDS, "kind": "file"}],
            "parser": "local",
            "notebook_id": notebook.id,
        },
    )

    assert response.status_code == 200, response.text
    body = response.json()
    [document_id] = body["document_ids"]
    assert body["queued"] == 1 and body["reused"] == 0
    assert api.dispatched[-1][0] == [document_id]

    document = await db_session.get(Document, document_id)
    assert document.title == "fluids.md"
    assert DocumentStatus.is_state(document.status, DocumentStatus.PENDING)
    facts = document.document_metadata["nextcloud"]
    assert facts["path"] == FLUIDS and facts["etag"] == "f1"
    assert facts["parser"] == "local" and facts["notebook_id"] == notebook.id
    assert document.folder_id is None
    assert await _links(db_session, notebook.id) == {("document", document_id)}


async def test_picking_the_same_unchanged_file_again_does_not_duplicate_it(
    api, db_session, make_owner, make_connector, make_notebook
):
    owner = await make_owner()
    connector = await make_connector(owner)
    first = await make_notebook(owner.workspace.id)
    second = await make_notebook(owner.workspace.id)
    client = api(owner.auth)
    pick = {
        "workspace_id": owner.workspace.id,
        "items": [{"path": FLUIDS, "kind": "file"}],
        "parser": "local",
    }

    [document_id] = (
        await client.post(
            _url(connector.id, "import"), json={**pick, "notebook_id": first.id}
        )
    ).json()["document_ids"]
    document = await db_session.get(Document, document_id)
    document.status = DocumentStatus.ready()
    await db_session.commit()
    dispatched_before = len(api.dispatched)

    again = (
        await client.post(
            _url(connector.id, "import"), json={**pick, "notebook_id": second.id}
        )
    ).json()

    assert again["document_ids"] == [document_id]
    assert again["reused"] == 1 and again["queued"] == 0
    assert len(api.dispatched) == dispatched_before
    assert await _links(db_session, second.id) == {("document", document_id)}
    ids = (
        await db_session.scalars(
            select(Document.id).where(Document.workspace_id == owner.workspace.id)
        )
    ).all()
    assert ids == [document_id]


async def test_a_file_changed_since_its_import_is_read_again_into_the_same_document(
    api, db_session, server, make_owner, make_connector
):
    owner = await make_owner()
    connector = await make_connector(owner)
    client = api(owner.auth)
    pick = {
        "workspace_id": owner.workspace.id,
        "items": [{"path": FLUIDS, "kind": "file"}],
        "parser": "local",
    }
    [document_id] = (await client.post(_url(connector.id, "import"), json=pick)).json()[
        "document_ids"
    ]
    document = await db_session.get(Document, document_id)
    document.status = DocumentStatus.ready()
    await db_session.commit()

    server.reply("PROPFIND", f"{DAV}{FLUIDS}", single_file_reply(FLUIDS, "f2"))
    again = (await client.post(_url(connector.id, "import"), json=pick)).json()

    assert again["document_ids"] == [document_id]
    assert again["queued"] == 1
    await db_session.refresh(document)
    assert document.document_metadata["nextcloud"]["etag"] == "f2"
    assert DocumentStatus.is_state(document.status, DocumentStatus.PENDING)


async def test_a_folder_keeps_its_structure_and_is_linked_as_one_source(
    api, db_session, make_owner, make_connector, make_notebook
):
    owner = await make_owner()
    connector = await make_connector(owner)
    notebook = await make_notebook(owner.workspace.id)

    body = (
        await api(owner.auth).post(
            _url(connector.id, "import"),
            json={
                "workspace_id": owner.workspace.id,
                "items": [{"path": "/Open Notebook", "kind": "folder"}],
                "parser": "local",
                "notebook_id": notebook.id,
            },
        )
    ).json()

    assert body["unsupported"] == ["/Open Notebook/Week 1/clip.mkv"]
    [root_id] = body["folder_ids"]
    root = await db_session.get(Folder, root_id)
    assert root.name == "Open Notebook" and root.parent_id is None
    documents = {
        d.title: d
        for d in await db_session.scalars(
            select(Document).where(Document.id.in_(body["document_ids"]))
        )
    }
    assert set(documents) == {"Anatomy notes.pdf", "fluids.md"}
    assert documents["Anatomy notes.pdf"].folder_id == root_id
    week = await db_session.get(Folder, documents["fluids.md"].folder_id)
    assert week.name == "Week 1" and week.parent_id == root_id
    assert await _links(db_session, notebook.id) == {("folder", root_id)}


async def test_the_preview_counts_what_a_folder_would_bring(
    api, make_owner, make_connector
):
    owner = await make_owner()
    connector = await make_connector(owner)

    body = (
        await api(owner.auth).post(
            _url(connector.id, "import/preview"),
            json={
                "workspace_id": owner.workspace.id,
                "items": [{"path": "/Open Notebook", "kind": "folder"}],
                "parser": "local",
            },
        )
    ).json()

    assert body["file_count"] == 2
    assert body["unsupported_count"] == 1
    assert body["over_limit"] is False
    assert body["max_files"] == 200


async def test_importing_needs_the_notebook_to_be_in_the_connectors_workspace(
    api, make_owner, make_connector, make_notebook
):
    owner = await make_owner()
    other = await make_owner()
    connector = await make_connector(owner)
    foreign = await make_notebook(other.workspace.id)

    response = await api(owner.auth).post(
        _url(connector.id, "import"),
        json={
            "workspace_id": owner.workspace.id,
            "items": [{"path": FLUIDS, "kind": "file"}],
            "notebook_id": foreign.id,
        },
    )
    assert response.status_code == 404


async def test_refresh_requeues_only_when_the_etag_changed(
    api, db_session, server, make_owner, make_connector
):
    owner = await make_owner()
    connector = await make_connector(owner)
    client = api(owner.auth)
    [document_id] = (
        await client.post(
            _url(connector.id, "import"),
            json={
                "workspace_id": owner.workspace.id,
                "items": [{"path": FLUIDS, "kind": "file"}],
                "parser": "local",
            },
        )
    ).json()["document_ids"]
    document = await db_session.get(Document, document_id)
    document.status = DocumentStatus.ready()
    await db_session.commit()

    refresh = _url(connector.id, f"documents/{document_id}/refresh")
    assert (await client.post(refresh)).json() == {"status": "unchanged"}

    server.reply("PROPFIND", f"{DAV}{FLUIDS}", single_file_reply(FLUIDS, "f9"))
    assert (await client.post(refresh)).json() == {"status": "queued"}
    assert api.dispatched[-1][0] == [document_id]
