"""An HTTP client over the real app whose caller can be switched mid-test.

The session dependency rides the test's transactional ``db_session``; the auth
dependency answers as whoever ``client.act_as(user)`` last named, or as nobody
(401) after ``client.act_as(None)``, for the public routes.
"""

from __future__ import annotations

from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager

import httpx
from fastapi import HTTPException
from httpx import ASGITransport
from sqlalchemy.ext.asyncio import AsyncSession

from app.app import app, limiter
from app.auth.context import AuthContext
from app.db import User, get_async_session
from app.users import get_auth_context, require_session_context


class ActingClient(httpx.AsyncClient):
    acting: User | None = None

    def act_as(self, user: User | None) -> ActingClient:
        self.acting = user
        return self


@asynccontextmanager
async def acting_client(db_session: AsyncSession) -> AsyncGenerator[ActingClient, None]:
    limiter.enabled = False

    async def override_session() -> AsyncGenerator[AsyncSession, None]:
        yield db_session

    holder: dict[str, ActingClient] = {}

    async def override_auth() -> AuthContext:
        user = holder["client"].acting
        if user is None:
            raise HTTPException(status_code=401, detail="Unauthorized")
        return AuthContext.session(user)

    previous = app.dependency_overrides.copy()
    app.dependency_overrides[get_async_session] = override_session
    app.dependency_overrides[get_auth_context] = override_auth
    app.dependency_overrides[require_session_context] = override_auth
    try:
        async with ActingClient(
            transport=ASGITransport(app=app), base_url="http://test", timeout=30.0
        ) as client:
            holder["client"] = client
            yield client
    finally:
        app.dependency_overrides.clear()
        app.dependency_overrides.update(previous)
