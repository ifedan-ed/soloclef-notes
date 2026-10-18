"""The generic connector routes and Nextcloud: no pasted logins, and removal revokes."""

from __future__ import annotations

from types import SimpleNamespace

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI

from app.connectors.nextcloud.connector_credentials import (
    account_from_config,
    stored_config,
)
from app.connectors.nextcloud.login_flow import revocation
from app.db import SearchSourceConnector, get_async_session
from app.routes import search_source_connectors_routes
from app.users import get_auth_context
from tests.unit.connectors.nextcloud.fake_login_server import FakeLoginServer

pytestmark = pytest.mark.integration


@pytest_asyncio.fixture
async def generic_api(db_session):
    app = FastAPI()
    app.include_router(search_source_connectors_routes.router, prefix="/api/v1")
    acting = SimpleNamespace(auth=None)

    async def session_override():
        yield db_session

    app.dependency_overrides[get_async_session] = session_override
    app.dependency_overrides[get_auth_context] = lambda: acting.auth
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://test"
    ) as client:

        def as_user(auth):
            acting.auth = auth
            return client

        yield as_user


async def test_a_pasted_nextcloud_login_is_refused(generic_api, db_session, make_owner):
    owner = await make_owner()
    response = await generic_api(owner.auth).post(
        "/api/v1/search-source-connectors",
        params={"workspace_id": owner.workspace.id},
        json={
            "name": "Nextcloud",
            "connector_type": "NEXTCLOUD_CONNECTOR",
            "is_indexable": False,
            "config": {
                "server_url": "https://attacker.example.net",
                "username": "x",
                "app_password": "y",
            },
        },
    )
    assert response.status_code == 400


async def test_a_rename_cannot_swap_the_stored_login(
    generic_api, db_session, make_owner, make_connector
):
    owner = await make_owner()
    connector = await make_connector(owner)
    response = await generic_api(owner.auth).put(
        f"/api/v1/search-source-connectors/{connector.id}",
        json={
            "name": "My Nextcloud",
            "config": {
                "server_url": "https://attacker.example.net",
                "app_password": "z",
            },
        },
    )
    assert response.status_code == 200, response.text
    row = await db_session.get(SearchSourceConnector, connector.id)
    assert row.name == "My Nextcloud"
    assert account_from_config(row.config).base_url == "https://cloud.example.com"


async def test_deleting_the_connector_revokes_its_app_password(
    generic_api, db_session, make_owner, make_connector, monkeypatch
):
    cloud = FakeLoginServer(login_name="alice")
    cloud.valid_passwords.add("app-pw")
    monkeypatch.setattr(revocation, "NextcloudLoginApi", lambda base: cloud.api(base))
    owner = await make_owner()
    connector = await make_connector(owner)
    connector.config = stored_config("https://cloud.example.com", "alice", "app-pw")
    await db_session.flush()

    response = await generic_api(owner.auth).delete(
        f"/api/v1/search-source-connectors/{connector.id}"
    )
    assert response.status_code == 200, response.text
    assert cloud.revoked == ["app-pw"]
