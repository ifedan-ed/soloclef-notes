"""Fixtures for the hand-off: the Drive fixtures (stand-in Google, owners,
notebooks) and an app with the Drive and hand-off routes, codes and
tickets kept in memory, and the rate limits off."""

from __future__ import annotations

from types import SimpleNamespace

import httpx
import pytest_asyncio
from fastapi import FastAPI

from app.app_handoff import codes as handoff_codes, routes as handoff_routes
from app.auth.context import AuthContext
from app.connectors.google_drive.connect import routes as connect_routes
from app.connectors.google_drive.notebook_import import routes as import_routes
from app.db import get_async_session
from app.rate_limiter import limiter
from app.users import get_auth_context, require_session_context
from tests.integration.connectors.google_drive.conftest import (  # noqa: F401 - fixtures
    google,
    make_notebook,
    make_owner,
)


@pytest_asyncio.fixture
async def handoff_api(db_session, google, monkeypatch):  # noqa: F811
    monkeypatch.setattr(
        handoff_codes,
        "_codes",
        handoff_codes.MemoryStore(handoff_codes.CODE_LIFETIME_SECONDS),
    )
    monkeypatch.setattr(
        handoff_codes,
        "_tickets",
        handoff_codes.MemoryStore(handoff_codes.TICKET_LIFETIME_SECONDS),
    )
    monkeypatch.setattr(limiter, "enabled", False)
    app = FastAPI()
    app.state.limiter = limiter
    for router in (connect_routes.router, import_routes.router, handoff_routes.router):
        app.include_router(router, prefix="/api/v1")
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

        def as_user(auth: AuthContext | None):
            acting.auth = auth
            return client

        as_user.dispatched = dispatched
        yield as_user
