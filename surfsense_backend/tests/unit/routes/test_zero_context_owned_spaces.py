"""The Zero context names the workspaces the user owns, among those they belong to.

The web's Zero chat queries let only a workspace's owner read a thread whose
creator is gone (surfsense_web/zero/queries/thread-access.ts), as the REST
thread routes do; this is where that list comes from.
"""

from __future__ import annotations

from types import SimpleNamespace
from uuid import uuid4

import pytest
from fastapi import FastAPI
from starlette.testclient import TestClient

from app.auth.context import AuthContext
from app.db import get_async_session
from app.routes import zero_context_routes
from app.users import get_auth_context

pytestmark = pytest.mark.unit

USER = SimpleNamespace(id=uuid4(), is_superuser=False, email="probe@example.org")


class _Session:
    def __init__(self, owned: list[int]) -> None:
        self.owned = owned
        self.statements: list[str] = []

    async def execute(self, statement):
        self.statements.append(str(statement.compile()))
        owned = self.owned
        return SimpleNamespace(scalars=lambda: SimpleNamespace(all=lambda: owned))


def _client(session: _Session, allowed: list[int], monkeypatch) -> TestClient:
    async def allowed_ids(_session, _auth):
        return allowed

    monkeypatch.setattr(zero_context_routes, "get_allowed_read_space_ids", allowed_ids)
    app = FastAPI()
    app.include_router(zero_context_routes.router)

    async def session_dep():
        yield session

    app.dependency_overrides[get_async_session] = session_dep
    app.dependency_overrides[get_auth_context] = lambda: AuthContext.session(USER)
    return TestClient(app)


def test_the_context_names_owned_workspaces_among_the_allowed(monkeypatch) -> None:
    session = _Session(owned=[7])
    body = _client(session, [7, 8], monkeypatch).get("/zero/context").json()
    assert body == {
        "userId": str(USER.id),
        "allowedSpaceIds": [7, 8],
        "ownedSpaceIds": [7],
    }
    [statement] = session.statements
    assert "workspaces.user_id" in statement
    assert "workspaces.id IN" in statement


def test_no_workspace_means_no_query_and_none_owned(monkeypatch) -> None:
    session = _Session(owned=[7])
    body = _client(session, [], monkeypatch).get("/zero/context").json()
    assert body["ownedSpaceIds"] == []
    assert session.statements == []
