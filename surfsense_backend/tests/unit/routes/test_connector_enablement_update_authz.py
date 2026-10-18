"""PUT /search-source-connectors/{id}?workspace_id= writes that workspace's settings only where allowed.

``is_indexable`` and the periodic-indexing fields of an account connection
are one workspace's enablement settings. The connector's owner could name any
workspace id and get a row there, which the schedule checker then indexed
into. Now the connector must be usable in that workspace (its owner a member,
not switched off there) and the caller must be allowed to update connectors
there, before any row is read or made.
"""

from __future__ import annotations

from types import SimpleNamespace
from uuid import uuid4

import pytest
from fastapi import FastAPI, HTTPException
from starlette.testclient import TestClient

from app.auth.context import AuthContext
from app.db import SearchSourceConnectorType, get_async_session
from app.routes import search_source_connectors_routes as routes
from app.users import get_auth_context

pytestmark = pytest.mark.unit

OWNER = SimpleNamespace(id=uuid4(), is_superuser=False, email="owner@example.org")
SETTINGS = {"periodic_indexing_enabled": True, "indexing_frequency_minutes": 60}


class _Session:
    def __init__(self, connector) -> None:
        self.connector = connector
        self.added: list = []
        self.commits = 0

    async def execute(self, _statement):
        connector = self.connector
        return SimpleNamespace(scalars=lambda: SimpleNamespace(first=lambda: connector))

    def add(self, row) -> None:
        self.added.append(row)

    async def commit(self) -> None:
        self.commits += 1

    async def refresh(self, _row) -> None:
        pass

    async def rollback(self) -> None:
        pass


@pytest.fixture
def world(monkeypatch):
    connector = SimpleNamespace(
        id=5,
        connector_type=SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
        user_id=OWNER.id,
        workspace_id=None,
        is_indexable=True,
        config={},
    )
    session = _Session(connector)
    rows_read: list[tuple[int, int]] = []
    checks: dict[str, list] = {"enabled": [], "permission": []}
    usable = {"value": True}
    permitted = {"value": True}

    async def is_enabled(_session, _connector, workspace_id):
        checks["enabled"].append(workspace_id)
        return usable["value"]

    async def check_permission(_session, _auth, workspace_id, permission, *_args):
        checks["permission"].append((workspace_id, permission))
        if not permitted["value"]:
            raise HTTPException(status_code=403, detail="nope")

    async def get_enablement_row(_session, connector_id, workspace_id):
        rows_read.append((connector_id, workspace_id))
        return None

    monkeypatch.setattr(routes, "is_enabled_in_workspace", is_enabled)
    monkeypatch.setattr(routes, "check_permission", check_permission)
    monkeypatch.setattr(routes, "get_enablement_row", get_enablement_row)

    app = FastAPI()
    app.include_router(routes.router)

    async def session_dep():
        yield session

    app.dependency_overrides[get_async_session] = session_dep
    app.dependency_overrides[get_auth_context] = lambda: AuthContext.session(OWNER)
    return SimpleNamespace(
        client=TestClient(app, raise_server_exceptions=False),
        session=session,
        rows_read=rows_read,
        checks=checks,
        usable=usable,
        permitted=permitted,
    )


def _put(world, workspace_id: int):
    return world.client.put(
        f"/search-source-connectors/5?workspace_id={workspace_id}", json=SETTINGS
    )


def test_a_workspace_the_connector_is_not_usable_in_gets_no_settings(world) -> None:
    world.usable["value"] = False
    res = _put(world, 99)
    assert res.status_code == 404
    assert world.checks["enabled"] == [99]
    assert world.rows_read == []
    assert world.session.added == []
    assert world.session.commits == 0


def test_without_permission_to_update_connectors_there_nothing_is_written(
    world,
) -> None:
    world.permitted["value"] = False
    res = _put(world, 7)
    assert res.status_code == 403
    assert world.checks["permission"] == [
        (7, routes.Permission.CONNECTORS_UPDATE.value)
    ]
    assert world.rows_read == []
    assert world.session.added == []
    assert world.session.commits == 0
