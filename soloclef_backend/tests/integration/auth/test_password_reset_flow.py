"""Forgot password -> email -> new password, over HTTP on real Postgres.

The mail server and Cloudflare are the external boundaries and are faked;
tokens, hashing, routing, rate limits and the user rows are real.
"""

from __future__ import annotations

import asyncio
import re
import uuid
from collections.abc import AsyncGenerator

import httpx
import pytest
import pytest_asyncio
from fastapi_users.password import PasswordHelper
from httpx import ASGITransport
from sqlalchemy.ext.asyncio import AsyncSession

import app.app as app_module
import app.users as users_module
from app.app import app
from app.auth import turnstile
from app.config import config
from app.db import User, get_async_session
from app.password_reset import delivery

pytestmark = pytest.mark.integration

REAL_RATE_LIMIT = app_module._check_rate_limit
OLD = "old-password-123"
NEW = "brand new password 456"


class Outbox:
    def __init__(self):
        self.sent = []

    async def send(self, message):
        self.sent.append(message)

    def token_for(self, address: str) -> str:
        mail = next(m for m in reversed(self.sent) if m.to == address)
        return re.search(r"#token=([\w.\-]+)", mail.text_body).group(1)


@pytest.fixture
def outbox(monkeypatch):
    box = Outbox()
    pending: list[asyncio.Task] = []
    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: True)
    monkeypatch.setattr(delivery, "get_mailer", lambda: box)
    monkeypatch.setattr(delivery, "_within_address_cap", lambda email: True)

    def schedule(user_id, email, token):
        pending.append(
            asyncio.create_task(delivery.send_reset_email(user_id, email, token))
        )

    monkeypatch.setattr(users_module, "schedule_reset_email", schedule)
    box.pending = pending
    return box


@pytest.fixture(autouse=True)
def _no_side_sessions(monkeypatch):
    # These open their own connections, which would wait on this test's
    # uncommitted user row; what they do is covered elsewhere.
    revoked: list = []

    async def create_refresh_token(user_id):
        return "refresh-token"

    async def revoke_all(user_id):
        revoked.append(user_id)

    async def on_after_login(self, user, request=None, response=None):
        return None

    monkeypatch.setattr(users_module, "create_refresh_token", create_refresh_token)
    monkeypatch.setattr(users_module, "revoke_all_user_tokens", revoke_all)
    monkeypatch.setattr(users_module.UserManager, "on_after_login", on_after_login)
    monkeypatch.setattr(app_module, "_check_rate_limit", lambda *a, **k: None)
    monkeypatch.setattr(config, "TURNSTILE_SECRET_KEY", "")
    return revoked


@pytest_asyncio.fixture
async def person(db_session: AsyncSession) -> User:
    user = User(
        id=uuid.uuid4(),
        email=f"reset-{uuid.uuid4().hex[:8]}@example.com",
        hashed_password=PasswordHelper().hash(OLD),
        is_active=True,
        is_superuser=False,
        is_verified=True,
    )
    db_session.add(user)
    await db_session.flush()
    return user


@pytest_asyncio.fixture
async def client(db_session: AsyncSession) -> AsyncGenerator[httpx.AsyncClient, None]:
    async def override_session() -> AsyncGenerator[AsyncSession, None]:
        yield db_session

    previous = app.dependency_overrides.copy()
    app.dependency_overrides[get_async_session] = override_session
    # A browser sends Origin on every POST; without it, the session cookie a
    # sign-in sets makes the next POST fail the CSRF origin check.
    async with httpx.AsyncClient(
        transport=ASGITransport(app=app),
        base_url="http://test",
        headers={"Origin": "http://test"},
        timeout=30.0,
    ) as http:
        yield http
    app.dependency_overrides.clear()
    app.dependency_overrides.update(previous)


async def _forgot(client, email, **headers):
    return await client.post(
        "/auth/forgot-password", json={"email": email}, headers=headers
    )


async def _login(client, email, password, **headers):
    return await client.post(
        "/auth/jwt/login",
        data={"username": email, "password": password, "grant_type": "password"},
        headers=headers,
    )


async def _settle(outbox):
    await asyncio.gather(*outbox.pending)


async def test_forgot_then_reset_changes_the_password(
    client, person, outbox, _no_side_sessions
):
    response = await _forgot(client, person.email)
    assert response.status_code == 202
    await _settle(outbox)
    assert [m.to for m in outbox.sent] == [person.email]
    mail = outbox.sent[0]
    assert mail.subject == "Reset your Soloclef Notes password"
    assert "/reset-password#token=" in mail.text_body

    token = outbox.token_for(person.email)
    reset = await client.post(
        "/auth/reset-password", json={"token": token, "password": NEW}
    )
    assert reset.status_code == 200
    assert _no_side_sessions == [person.id]  # every other session ended

    assert (await _login(client, person.email, NEW)).status_code == 200
    old = await _login(client, person.email, OLD)
    assert old.status_code == 400


async def test_a_reset_link_works_only_once(client, person, outbox):
    await _forgot(client, person.email)
    await _settle(outbox)
    token = outbox.token_for(person.email)
    first = await client.post(
        "/auth/reset-password", json={"token": token, "password": NEW}
    )
    again = await client.post(
        "/auth/reset-password", json={"token": token, "password": "another one 789"}
    )
    assert first.status_code == 200
    assert again.status_code == 400
    assert again.json()["detail"] == "RESET_PASSWORD_BAD_TOKEN"


async def test_an_expired_link_is_refused(client, person, outbox, monkeypatch):
    monkeypatch.setattr(
        users_module.UserManager, "reset_password_token_lifetime_seconds", 1
    )
    await _forgot(client, person.email)
    await _settle(outbox)
    token = outbox.token_for(person.email)
    await asyncio.sleep(2.1)
    response = await client.post(
        "/auth/reset-password", json={"token": token, "password": NEW}
    )
    assert response.status_code == 400
    assert response.json()["detail"] == "RESET_PASSWORD_BAD_TOKEN"


async def test_a_forged_token_is_refused(client, person):
    response = await client.post(
        "/auth/reset-password",
        json={"token": "eyJhbGciOiJIUzI1NiJ9.e30.x", "password": NEW},
    )
    assert response.status_code == 400


async def test_an_unknown_address_gets_the_same_answer_and_no_mail(
    client, person, outbox
):
    known = await _forgot(client, person.email)
    unknown = await _forgot(client, f"nobody-{uuid.uuid4().hex[:6]}@example.com")
    await _settle(outbox)
    assert (known.status_code, known.content) == (unknown.status_code, unknown.content)
    assert [m.to for m in outbox.sent] == [person.email]


async def test_a_weak_new_password_is_refused_with_a_reason(client, person, outbox):
    await _forgot(client, person.email)
    await _settle(outbox)
    response = await client.post(
        "/auth/reset-password",
        json={"token": outbox.token_for(person.email), "password": "short"},
    )
    assert response.status_code == 400
    assert response.json()["detail"]["code"] == "RESET_PASSWORD_INVALID_PASSWORD"


async def test_turnstile_guards_reset_login_and_registration(
    client, person, outbox, monkeypatch
):
    monkeypatch.setattr(
        config, "TURNSTILE_SECRET_KEY", "1x0000000000000000000000000000000AA"
    )

    async def verify(token, remote_ip=None, **_):
        return turnstile.TurnstileResult(success=token == "good-token")

    monkeypatch.setattr(turnstile, "verify_turnstile_token", verify)

    missing = await _forgot(client, person.email)
    bad = await _forgot(client, person.email, **{"X-Turnstile-Token": "bad"})
    await _settle(outbox)
    assert (missing.status_code, missing.json()["detail"]) == (
        400,
        "TURNSTILE_REQUIRED",
    )
    assert (bad.status_code, bad.json()["detail"]) == (400, "TURNSTILE_FAILED")
    assert outbox.sent == []

    good = await _forgot(client, person.email, **{"X-Turnstile-Token": "good-token"})
    await _settle(outbox)
    assert good.status_code == 202 and len(outbox.sent) == 1

    assert (await _login(client, person.email, OLD)).status_code == 400
    ok = await _login(client, person.email, OLD, **{"X-Turnstile-Token": "good-token"})
    assert ok.status_code == 200

    register = await client.post(
        "/auth/register",
        json={"email": "new@example.com", "password": "long enough 1"},
    )
    assert (register.status_code, register.json()["detail"]) == (
        400,
        "TURNSTILE_REQUIRED",
    )


async def test_asking_for_reset_emails_is_rate_limited_per_ip(
    client, person, outbox, monkeypatch
):
    monkeypatch.setattr(app_module, "_check_rate_limit", REAL_RATE_LIMIT)
    ip = {"X-Real-IP": f"198.51.100.{uuid.uuid4().int % 250}"}
    codes = [(await _forgot(client, person.email, **ip)).status_code for _ in range(4)]
    await _settle(outbox)
    assert codes == [202, 202, 202, 429]
