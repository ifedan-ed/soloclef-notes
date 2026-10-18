"""Fixtures for Google Drive tests: the stand-in Google on loopback, owners, notebooks.

Google (consent, token, revoke, Drive v3) is ``tests/e2e/fakes/google_server.py``
served in-process; the app reaches it through the GOOGLE_* endpoint overrides,
so the real OAuth library, googleapiclient and httpx code paths all run.
"""

from __future__ import annotations

import uuid
from types import SimpleNamespace
from urllib.parse import parse_qs, urlsplit

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI

from app.auth.context import AuthContext
from app.config import config
from app.connectors.google_drive.connect import routes as connect_routes
from app.connectors.google_drive.notebook_import import routes as import_routes
from app.db import User, Workspace, get_async_session
from app.notebooks.persistence import Notebook
from app.routes.workspaces_routes import create_default_roles_and_membership
from app.users import get_auth_context, require_session_context
from tests.e2e.fakes.google_server import serve_in_thread

CLIENT_ID = "123456789012-it.apps.googleusercontent.com"
REDIRECT_URI = "http://test/api/v1/auth/google/drive/connector/callback"


@pytest.fixture
def google(monkeypatch):
    server, state = serve_in_thread()
    base = state.public_url
    monkeypatch.setenv("GOOGLE_OAUTH_AUTH_URI", f"{base}/o/oauth2/auth")
    monkeypatch.setenv("GOOGLE_OAUTH_TOKEN_URI", f"{base}/token")
    monkeypatch.setenv("GOOGLE_OAUTH_REVOKE_URI", f"{base}/revoke")
    monkeypatch.setenv("GOOGLE_DRIVE_API_ENDPOINT", f"{base}/drive/v3/")
    monkeypatch.setenv("OAUTHLIB_INSECURE_TRANSPORT", "1")
    monkeypatch.setenv("NO_PROXY", "127.0.0.1,localhost")
    monkeypatch.setenv("no_proxy", "127.0.0.1,localhost")
    monkeypatch.setattr(config, "GOOGLE_OAUTH_CLIENT_ID", CLIENT_ID)
    monkeypatch.setattr(config, "GOOGLE_OAUTH_CLIENT_SECRET", "it-client-secret")
    monkeypatch.setattr(config, "GOOGLE_DRIVE_REDIRECT_URI", REDIRECT_URI)
    monkeypatch.setattr(config, "GOOGLE_PICKER_API_KEY", "it-picker-key")
    monkeypatch.setattr(config, "GOOGLE_PICKER_APP_ID", None)
    monkeypatch.setattr(config, "NEXT_FRONTEND_URL", "http://app.test")
    monkeypatch.setattr(config, "SECRET_KEY", config.SECRET_KEY or "it-secret-key")
    yield state
    server.shutdown()


@pytest.fixture
def make_owner(db_session):
    async def build() -> SimpleNamespace:
        user = User(
            id=uuid.uuid4(),
            email=f"gd-{uuid.uuid4().hex[:8]}@surfsense.test",
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
def make_notebook(db_session):
    async def build(workspace_id: int) -> Notebook:
        notebook = Notebook(workspace_id=workspace_id, title="Notebook")
        db_session.add(notebook)
        await db_session.flush()
        return notebook

    return build


@pytest_asyncio.fixture
async def api(db_session, google):
    app = FastAPI()
    app.include_router(connect_routes.router, prefix="/api/v1")
    app.include_router(import_routes.router, prefix="/api/v1")
    acting = SimpleNamespace(auth=None)
    dispatched: list[tuple] = []

    async def session_override():
        yield db_session

    app.dependency_overrides[get_async_session] = session_override
    app.dependency_overrides[get_auth_context] = lambda: acting.auth
    app.dependency_overrides[require_session_context] = lambda: acting.auth
    app.dependency_overrides[import_routes.get_import_dispatcher] = lambda: (
        lambda *args: dispatched.append(args)
    )
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://test"
    ) as client:

        def as_user(auth: AuthContext):
            acting.auth = auth
            return client

        as_user.dispatched = dispatched
        yield as_user


def consent(google_state, auth_url: str, decision: str = "allow") -> str:
    """Click Allow (or Cancel) on the stand-in consent page; the redirect back."""
    query = {k: v[0] for k, v in parse_qs(urlsplit(auth_url).query).items()}
    response = httpx.post(
        f"{google_state.public_url}/o/oauth2/decide",
        data={**query, "decision": decision},
        follow_redirects=False,
    )
    assert response.status_code == 302
    return response.headers["location"]


async def connect(client, owner, google_state, return_path: str | None = None):
    """Connect through the whole consent round trip; the app's final redirect."""
    started = await client.post(
        f"/api/v1/workspaces/{owner.workspace.id}/google-drive/connect",
        json={"return_path": return_path},
    )
    assert started.status_code == 200, started.text
    back = consent(google_state, started.json()["auth_url"])
    parts = urlsplit(back)
    assert f"{parts.scheme}://{parts.netloc}{parts.path}" == REDIRECT_URI
    return await client.get(f"{parts.path}?{parts.query}", follow_redirects=False)


@pytest.fixture
def task_sessions_in_test_transaction(db_session, monkeypatch):
    """The indexer's per-file sessions become the test's session, one at a time.

    Parallel indexing opens ``get_celery_session_maker()()`` per file; handing
    each the test session (serialised, since a connection runs one statement at
    a time) keeps their commits inside the per-test rollback.
    """
    import asyncio
    import contextlib

    lock = asyncio.Lock()

    @contextlib.asynccontextmanager
    async def _session():
        async with lock:
            yield db_session

    monkeypatch.setattr(
        "app.tasks.celery_tasks.get_celery_session_maker", lambda: _session
    )
    return db_session
