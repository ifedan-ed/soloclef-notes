"""Which documents offer "Refresh from Nextcloud", and to whom.

Only a file picked from Nextcloud can be refreshed, and only through its
account's connection, which only that connection's owner can use.
"""

from __future__ import annotations

import pytest

from app.auth.context import AuthContext
from app.db import Document, DocumentType

from .conftest import FLUIDS

pytestmark = pytest.mark.integration


def _list_url(workspace_id: int) -> str:
    return f"/api/v1/workspaces/{workspace_id}/nextcloud/refreshable-documents"


async def _import(client, connector_id: int, workspace_id: int) -> int:
    reply = await client.post(
        f"/api/v1/connectors/{connector_id}/nextcloud/import",
        json={
            "workspace_id": workspace_id,
            "items": [{"path": FLUIDS, "kind": "file"}],
            "parser": "local",
        },
    )
    [document_id] = reply.json()["document_ids"]
    return document_id


async def test_a_picked_file_is_offered_through_its_connection(
    api, db_session, make_owner, make_connector
):
    owner = await make_owner()
    connector = await make_connector(owner)
    client = api(owner.auth)
    document_id = await _import(client, connector.id, owner.workspace.id)
    uploaded = Document(
        title="upload.md",
        document_type=DocumentType.FILE,
        content="x",
        content_hash="refreshable-upload",
        workspace_id=owner.workspace.id,
        document_metadata={"source": "upload"},
    )
    db_session.add(uploaded)
    await db_session.flush()

    reply = await client.get(_list_url(owner.workspace.id))

    assert reply.status_code == 200
    assert reply.json() == {
        "documents": [{"document_id": document_id, "connector_id": connector.id}]
    }


async def test_a_member_without_the_connection_is_offered_nothing(
    api, make_owner, make_connector, make_user, add_member
):
    owner = await make_owner()
    connector = await make_connector(owner)
    await _import(api(owner.auth), connector.id, owner.workspace.id)
    member = await make_user()
    await add_member(owner.workspace, member)

    reply = await api(AuthContext.session(member)).get(_list_url(owner.workspace.id))

    assert reply.status_code == 200
    assert reply.json() == {"documents": []}


async def test_a_stranger_cannot_list_another_workspace(api, make_owner):
    owner = await make_owner()
    stranger = await make_owner()

    reply = await api(stranger.auth).get(_list_url(owner.workspace.id))

    assert reply.status_code in (403, 404)
