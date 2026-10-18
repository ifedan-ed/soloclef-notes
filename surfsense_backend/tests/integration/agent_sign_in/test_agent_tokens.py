"""An agent's Authentik sign-in over HTTP, on real Postgres.

Covers: a genuine token acts as the account with the same verified email
(operator@example.com as operator, email@example.com as email) or the one its subject is linked to;
expired, wrong-audience and account-less tokens are refused; the agent is
gated like an API key (only workspaces with API access on, no session-only
actions); and the "Connect your agent" details are for signed-in people.
"""

from __future__ import annotations

import time
import uuid
from collections.abc import AsyncGenerator

import httpx
import pytest
import pytest_asyncio
from httpx import ASGITransport
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncSession

from app.agent_sign_in import principal
from app.agent_sign_in.access_token import AgentTokenVerifier
from app.agent_sign_in.settings import AgentSignInSettings
from app.app import app, limiter
from app.auth_oidc.account_link import identity_links
from app.auth_oidc.discovery import ProviderDirectory
from app.db import User, Workspace, get_async_session
from app.routes.workspaces_routes import create_default_roles_and_membership
from tests.utils.fake_oidc_provider import FakeOidcProvider

pytestmark = pytest.mark.integration

limiter.enabled = False

ISSUER = "https://sso.test/application/o/soloclef-mcp/"
SETTINGS = AgentSignInSettings(
    issuer=ISSUER, client_id="soloclef-mcp", client_secret="shown-in-dialog"
)

OAUTH_ACCOUNT_DDL = """
CREATE TABLE IF NOT EXISTS oauth_account (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    oauth_name varchar(100) NOT NULL,
    access_token varchar(1024) NOT NULL,
    expires_at integer,
    refresh_token varchar(1024),
    account_id varchar(320) NOT NULL,
    account_email varchar(320) NOT NULL
)
"""


@pytest.fixture
def provider(monkeypatch) -> FakeOidcProvider:
    fake = FakeOidcProvider(issuer=ISSUER, client_id="soloclef-mcp")
    verifier = AgentTokenVerifier(
        SETTINGS, ProviderDirectory(SETTINGS.discovery_url, transport=fake.transport)
    )
    monkeypatch.setattr(principal, "_default_verifier", lambda: verifier)
    monkeypatch.setattr(principal, "agent_settings", lambda: SETTINGS)
    return fake


@pytest_asyncio.fixture
async def client(db_session: AsyncSession) -> AsyncGenerator[httpx.AsyncClient, None]:
    await db_session.execute(text(OAUTH_ACCOUNT_DDL))

    async def override_session() -> AsyncGenerator[AsyncSession, None]:
        yield db_session

    previous = app.dependency_overrides.copy()
    app.dependency_overrides[get_async_session] = override_session
    try:
        async with httpx.AsyncClient(
            transport=ASGITransport(app=app), base_url="http://test", timeout=30.0
        ) as c:
            yield c
    finally:
        app.dependency_overrides.clear()
        app.dependency_overrides.update(previous)


async def account(session: AsyncSession, email: str) -> tuple[User, Workspace]:
    user = User(
        id=uuid.uuid4(),
        email=email,
        hashed_password="x",
        is_active=True,
        is_superuser=False,
        is_verified=True,
    )
    session.add(user)
    await session.flush()
    space = Workspace(name=f"{email} space", user_id=user.id)
    session.add(space)
    await session.flush()
    await create_default_roles_and_membership(session, space.id, user.id)
    await session.flush()
    return user, space


def bearer(provider: FakeOidcProvider, **changes) -> dict[str, str]:
    now = int(time.time())
    claims = {
        "iss": ISSUER,
        "sub": f"sub-{uuid.uuid4().hex[:6]}",
        "aud": "soloclef-mcp",
        "azp": "soloclef-mcp",
        "iat": now,
        "exp": now + 3600,
        "scope": "openid email profile offline_access",
        "email": "operator@example.com",
        "email_verified": True,
        "soloclef_notes_access": True,
    }
    claims.update(changes)
    claims = {k: v for k, v in claims.items() if v is not None}
    return {"Authorization": f"Bearer {provider.sign(claims)}"}


async def test_each_email_acts_as_its_own_account(client, provider, db_session):
    operator, _ = await account(db_session, "operator@example.com")
    email_account, _ = await account(db_session, "email@example.com")

    r = await client.get("/verify-token", headers=bearer(provider))
    assert r.status_code == 200, r.text
    assert r.json() == {
        "message": "Token is valid",
        "method": "agent",
        "user_id": str(operator.id),
    }
    r = await client.get(
        "/verify-token", headers=bearer(provider, email="email@example.com")
    )
    assert r.json()["user_id"] == str(email_account.id)


async def test_a_linked_subject_wins_over_the_email(client, provider, db_session):
    operator, _ = await account(db_session, "operator@example.com")
    other, _ = await account(db_session, "renamed@example.com")
    await db_session.execute(
        identity_links.insert().values(
            id=uuid.uuid4(),
            user_id=other.id,
            oauth_name="authentik",
            access_token="",
            account_id="linked-subject",
            account_email="renamed@example.com",
        )
    )
    r = await client.get(
        "/verify-token", headers=bearer(provider, sub="linked-subject")
    )
    assert r.json()["user_id"] == str(other.id)
    assert r.json()["user_id"] != str(operator.id)


@pytest.mark.parametrize(
    "change",
    [
        {"exp": int(time.time()) - 600},
        {"aud": "soloclef-notes", "azp": "soloclef-notes"},
        {"iss": "https://sso.test/application/o/notes/"},
    ],
    ids=["expired", "wrong audience", "another app's issuer"],
)
async def test_bad_tokens_are_refused(client, provider, db_session, change):
    await account(db_session, "operator@example.com")
    r = await client.get("/verify-token", headers=bearer(provider, **change))
    assert r.status_code == 401


async def test_an_unverified_or_unknown_email_gets_no_account(
    client, provider, db_session
):
    await account(db_session, "operator@example.com")
    r = await client.get(
        "/verify-token", headers=bearer(provider, email_verified=False)
    )
    assert r.status_code == 403
    r = await client.get(
        "/verify-token", headers=bearer(provider, email="stranger@example.com")
    )
    assert r.status_code == 403


async def test_any_notes_account_authentik_lets_in_may_connect(
    client, provider, db_session
):
    # ifee@ is not in notes-users; example-accounts-only lets her into Notes,
    # and the token's claim is all Notes looks at.
    ifee, _ = await account(db_session, "ifee@example.com")
    r = await client.get(
        "/verify-token",
        headers=bearer(
            provider, email="ifee@example.com", groups=["example-sso-members"]
        ),
    )
    assert r.status_code == 200, r.text
    assert r.json()["user_id"] == str(ifee.id)


@pytest.mark.parametrize("access", [False, None], ids=["access taken away", "no claim"])
async def test_without_notes_access_the_agent_is_refused(
    client, provider, db_session, access
):
    # What the next refresh carries once Authentik no longer lets the person
    # into Notes: refused with 403 and no challenge, so no sign-in loop.
    await account(db_session, "operator@example.com")
    r = await client.get(
        "/verify-token",
        headers=bearer(provider, groups=["notes-users"], soloclef_notes_access=access),
    )
    assert r.status_code == 403
    assert "www-authenticate" not in r.headers


async def test_the_agent_is_gated_like_an_api_key(client, provider, db_session):
    _, space = await account(db_session, "operator@example.com")
    r = await client.get("/api/v1/workspaces", headers=bearer(provider))
    assert r.status_code == 200, r.text
    assert [w["id"] for w in r.json()] == []

    space.api_access_enabled = True
    await db_session.flush()
    r = await client.get("/api/v1/workspaces", headers=bearer(provider))
    assert [w["id"] for w in r.json()] == [space.id]

    # Session-only actions refuse it: no new API keys, no dialog secrets.
    r = await client.post("/api/v1/pats", headers=bearer(provider), json={"label": "x"})
    assert r.status_code == 403
    r = await client.get("/api/v1/agent-sign-in", headers=bearer(provider))
    assert r.status_code == 403
