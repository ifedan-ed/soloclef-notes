"""Billing fixtures on the transactional test database.

Billing opens its own sessions; here they are siblings of the test's
``db_session`` (same connection, savepoints), so they see its uncommitted rows
and everything still rolls back at the end of each test.
"""

from __future__ import annotations

import uuid
from collections.abc import AsyncGenerator

import httpx
import pytest
import pytest_asyncio
from httpx import ASGITransport
from sqlalchemy.ext.asyncio import AsyncSession

from app.app import app, limiter
from app.auth.context import AuthContext
from app.billing import meter
from app.billing.seed import ensure_catalog
from app.billing.settings_store import save_settings
from app.db import User, get_async_session
from app.users import get_auth_context

limiter.enabled = False


@pytest_asyncio.fixture(autouse=True)
async def billing_db(db_session: AsyncSession, monkeypatch):
    await ensure_catalog(db_session)
    monkeypatch.setattr(
        meter, "_default_factory", lambda: meter.sibling_sessions(db_session)
    )
    monkeypatch.setattr("app.config.config.BILLING_ADMIN_EMAILS", "owner@surfsense.net")
    return db_session


@pytest.fixture
def set_mode(db_session: AsyncSession):
    async def apply(mode: str, **values):
        await save_settings(db_session, values={"mode": mode, **values})

    return apply


@pytest.fixture
def new_user(db_session: AsyncSession):
    async def build(email: str | None = None) -> User:
        user = User(
            id=uuid.uuid4(),
            email=email or f"{uuid.uuid4().hex[:8]}@surfsense.net",
            hashed_password="hashed",
            is_active=True,
            is_superuser=False,
            is_verified=True,
        )
        db_session.add(user)
        await db_session.flush()
        return user

    return build


@pytest.fixture
def api(db_session: AsyncSession):
    """``async with api(user) as client`` — the app, signed in as ``user``
    (or anonymous with ``None``), on the test transaction."""

    class _Api:
        def __init__(self, user):
            self.user = user

        async def __aenter__(self) -> httpx.AsyncClient:
            async def override_session() -> AsyncGenerator[AsyncSession, None]:
                yield db_session

            self._previous = app.dependency_overrides.copy()
            app.dependency_overrides[get_async_session] = override_session
            if self.user is not None:
                user = self.user

                async def override_auth() -> AuthContext:
                    return AuthContext.session(user)

                app.dependency_overrides[get_auth_context] = override_auth
            self._client = httpx.AsyncClient(
                transport=ASGITransport(app=app), base_url="http://test", timeout=30.0
            )
            return await self._client.__aenter__()

        async def __aexit__(self, *exc):
            await self._client.__aexit__(*exc)
            app.dependency_overrides.clear()
            app.dependency_overrides.update(self._previous)

    return _Api
