"""Fixtures for Nextcloud import tests: an owner, a connector, a recorded server."""

from __future__ import annotations

import uuid
from types import SimpleNamespace
from urllib.parse import quote

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI

from app.auth.context import AuthContext
from app.connectors.nextcloud import routes as nextcloud_routes
from app.connectors.nextcloud.connector_credentials import stored_config
from app.db import (
    SearchSourceConnector,
    SearchSourceConnectorType,
    User,
    Workspace,
    get_async_session,
)
from app.notebooks.persistence import Notebook
from app.routes.workspaces_routes import create_default_roles_and_membership
from app.users import get_auth_context
from tests.unit.connectors.nextcloud.fake_server import (
    DAV,
    FakeNextcloud,
    multistatus,
)


@pytest.fixture(autouse=True)
def _configured_nextcloud(monkeypatch):
    from app.config import config

    monkeypatch.setattr(config, "NEXTCLOUD_BASE_URL", "https://cloud.example.com")


@pytest.fixture
def make_owner(db_session):
    async def build() -> SimpleNamespace:
        user = User(
            id=uuid.uuid4(),
            email=f"nc-{uuid.uuid4().hex[:8]}@surfsense.test",
            hashed_password="x",
            is_active=True,
            is_superuser=False,
            is_verified=True,
        )
        db_session.add(user)
        await db_session.flush()
        workspace = Workspace(name=f"Space {uuid.uuid4().hex[:6]}", user_id=user.id)
        db_session.add(workspace)
        await db_session.flush()
        await create_default_roles_and_membership(db_session, workspace.id, user.id)
        await db_session.flush()
        return SimpleNamespace(
            user=user, workspace=workspace, auth=AuthContext.session(user)
        )

    return build


@pytest.fixture
def make_connector(db_session):
    async def build(owner) -> SearchSourceConnector:
        connector = SearchSourceConnector(
            name="Nextcloud files",
            connector_type=SearchSourceConnectorType.NEXTCLOUD_CONNECTOR,
            is_indexable=False,
            config=stored_config("https://cloud.example.com", "alice", "app-pw"),
            workspace_id=owner.workspace.id,
            user_id=owner.user.id,
        )
        db_session.add(connector)
        await db_session.flush()
        return connector

    return build


@pytest.fixture
def make_notebook(db_session):
    async def build(workspace_id: int) -> Notebook:
        notebook = Notebook(workspace_id=workspace_id, title="Notebook")
        db_session.add(notebook)
        await db_session.flush()
        return notebook

    return build


FLUIDS = "/Open Notebook/Week 1/fluids.md"


def single_file_reply(path: str, etag: str) -> httpx.Response:
    href = "/remote.php/dav/files/alice" + quote(path)
    return httpx.Response(
        207,
        content=(
            '<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"><d:response>'
            f"<d:href>{href}</d:href><d:propstat><d:prop><d:resourcetype/>"
            "<d:getcontentlength>12</d:getcontentlength>"
            "<d:getcontenttype>text/markdown</d:getcontenttype>"
            f"<d:getetag>&quot;{etag}&quot;</d:getetag></d:prop>"
            "<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>"
            "</d:multistatus>"
        ).encode(),
    )


@pytest.fixture
def server() -> FakeNextcloud:
    fake = FakeNextcloud()
    fake.reply("PROPFIND", f"{DAV}/", multistatus("propfind_root.xml"))
    fake.reply(
        "PROPFIND", f"{DAV}/Open Notebook", multistatus("propfind_open_notebook.xml")
    )
    fake.reply(
        "PROPFIND", f"{DAV}/Open Notebook/Week 1", multistatus("propfind_week1.xml")
    )
    fake.reply("PROPFIND", f"{DAV}{FLUIDS}", single_file_reply(FLUIDS, "f1"))
    fake.reply("GET", f"{DAV}{FLUIDS}", httpx.Response(200, content=b"# Fluids\n"))
    return fake


@pytest_asyncio.fixture
async def api(db_session, server):
    app = FastAPI()
    app.include_router(nextcloud_routes.router, prefix="/api/v1")
    acting = SimpleNamespace(auth=None)
    dispatched: list[tuple[list[int], object, int]] = []

    async def session_override():
        yield db_session

    app.dependency_overrides[get_async_session] = session_override
    app.dependency_overrides[get_auth_context] = lambda: acting.auth
    app.dependency_overrides[nextcloud_routes.get_client_factory] = lambda: (
        lambda account: server.client()
    )
    app.dependency_overrides[nextcloud_routes.get_import_dispatcher] = lambda: (
        lambda ids, user_id, max_bytes: dispatched.append(
            (list(ids), user_id, max_bytes)
        )
    )
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://test"
    ) as client:

        def as_user(auth: AuthContext):
            acting.auth = auth
            return client

        as_user.dispatched = dispatched
        yield as_user
