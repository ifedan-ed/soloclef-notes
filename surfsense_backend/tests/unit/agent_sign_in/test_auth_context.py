"""The request's principal: an agent's Authentik token is an agent, or refused."""

from __future__ import annotations

import time
import uuid
from types import SimpleNamespace

import pytest
from fastapi import HTTPException
from starlette.requests import Request

from app.agent_sign_in import principal
from app.agent_sign_in.access_token import AgentTokenVerifier
from app.agent_sign_in.settings import AgentSignInSettings
from app.auth_oidc.discovery import ProviderDirectory
from app.users import get_auth_context
from tests.utils.fake_oidc_provider import FakeOidcProvider

pytestmark = pytest.mark.unit

ISSUER = "https://sso.test/application/o/soloclef-mcp/"
SETTINGS = AgentSignInSettings(issuer=ISSUER, client_id="soloclef-mcp")


@pytest.fixture
def provider(monkeypatch):
    fake = FakeOidcProvider(issuer=ISSUER, client_id="soloclef-mcp")
    verifier = AgentTokenVerifier(
        SETTINGS, ProviderDirectory(SETTINGS.discovery_url, transport=fake.transport)
    )
    monkeypatch.setattr(principal, "_default_verifier", lambda: verifier)
    return fake


def request_with(token: str) -> Request:
    return Request(
        {
            "type": "http",
            "method": "GET",
            "path": "/api/v1/workspaces",
            "headers": [(b"authorization", f"Bearer {token}".encode())],
            "query_string": b"",
        }
    )


class FakeSession:
    def __init__(self, user):
        self._user = user

    async def scalar(self, _statement):
        return self._user.id if self._user else None

    async def get(self, _model, _user_id):
        return self._user


def signed(provider, **changes) -> str:
    now = int(time.time())
    claims = {
        "iss": ISSUER,
        "sub": "subject-1",
        "aud": "soloclef-mcp",
        "azp": "soloclef-mcp",
        "iat": now,
        "exp": now + 600,
        "email": "you@example.com",
        "email_verified": True,
        "soloclef_notes_access": True,
    }
    claims.update(changes)
    return provider.sign(claims)


async def test_a_good_agent_token_is_an_agent_principal(provider):
    operator = SimpleNamespace(id=uuid.uuid4(), is_active=True)
    request = request_with(signed(provider))
    auth = await get_auth_context(
        request, session=FakeSession(operator), user_manager=None
    )
    assert auth.method == "agent"
    assert auth.user is operator
    assert auth.is_gated
    assert request.state.auth_context is auth


async def test_a_bad_agent_token_is_refused_without_trying_a_session(provider):
    request = request_with(signed(provider, aud="soloclef-notes", azp="soloclef-notes"))
    with pytest.raises(HTTPException) as refused:
        await get_auth_context(request, session=FakeSession(None), user_manager=None)
    assert refused.value.status_code == 401
    assert "agent" in refused.value.detail
