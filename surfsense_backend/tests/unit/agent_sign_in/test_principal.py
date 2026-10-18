"""A bearer token claiming the agent provider becomes an agent principal or a refusal."""

from __future__ import annotations

import time
import uuid
from types import SimpleNamespace

import httpx
import pytest
from fastapi import HTTPException

from app.agent_sign_in import principal
from app.agent_sign_in.access_token import AgentTokenVerifier
from app.agent_sign_in.principal import agent_user, is_agent_token
from app.agent_sign_in.settings import AgentSignInSettings, load_agent_sign_in_settings
from app.auth.context import AuthContext
from app.auth_oidc.discovery import ProviderDirectory
from tests.utils.fake_oidc_provider import FakeOidcProvider

pytestmark = pytest.mark.unit

ISSUER = "https://sso.test/application/o/soloclef-mcp/"
SETTINGS = AgentSignInSettings(issuer=ISSUER, client_id="soloclef-mcp")


@pytest.fixture
def provider():
    return FakeOidcProvider(issuer=ISSUER, client_id="soloclef-mcp")


@pytest.fixture
def verifier(provider):
    return AgentTokenVerifier(
        SETTINGS,
        ProviderDirectory(SETTINGS.discovery_url, transport=provider.transport),
    )


def token_for(provider, **changes):
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
        # What the blueprint's access mapping says for someone Authentik lets
        # into Notes.
        "soloclef_notes_access": True,
    }
    claims.update(changes)
    return provider.sign({k: v for k, v in claims.items() if v is not None})


class FakeSession:
    def __init__(self, user=None):
        self._user = user

    async def scalar(self, _statement):
        return None if self._user is None else self._user.id

    async def get(self, _model, _user_id):
        return self._user


async def test_a_good_token_acts_as_the_matching_account(provider, verifier):
    email_account = SimpleNamespace(id=uuid.uuid4(), is_active=True)
    found = await agent_user(
        token_for(provider),
        FakeSession(email_account),
        verifier=verifier,
        provider_name="authentik",
    )
    assert found is email_account


async def test_an_expired_token_is_401_with_a_bearer_challenge(provider, verifier):
    with pytest.raises(HTTPException) as refused:
        await agent_user(
            token_for(provider, exp=int(time.time()) - 600),
            FakeSession(),
            verifier=verifier,
            provider_name="authentik",
        )
    assert refused.value.status_code == 401
    assert refused.value.headers == {"WWW-Authenticate": 'Bearer error="invalid_token"'}


async def test_an_unreachable_provider_is_503_not_a_sign_in_prompt(provider):
    def down(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("sso.example.com unreachable", request=request)

    verifier = AgentTokenVerifier(
        SETTINGS,
        ProviderDirectory(SETTINGS.discovery_url, transport=httpx.MockTransport(down)),
    )
    with pytest.raises(HTTPException) as refused:
        await agent_user(
            token_for(provider),
            FakeSession(),
            verifier=verifier,
            provider_name="authentik",
        )
    assert refused.value.status_code == 503
    assert not refused.value.headers


async def test_a_genuine_sign_in_without_an_account_is_403(provider, verifier):
    with pytest.raises(HTTPException) as refused:
        await agent_user(
            token_for(provider, email="stranger@example.com"),
            FakeSession(None),
            verifier=verifier,
            provider_name="authentik",
        )
    assert refused.value.status_code == 403


@pytest.mark.parametrize(
    "email",
    ["ifee@example.com", "friend@gmail.com"],
    ids=["by the example.com policy", "by notes-users"],
)
async def test_anyone_authentik_lets_into_notes_acts_as_their_account(
    provider, verifier, email
):
    # No group or domain rule in Notes: whichever binding let the person in,
    # the token says so and that is enough.
    person = SimpleNamespace(id=uuid.uuid4(), is_active=True)
    found = await agent_user(
        token_for(provider, email=email, groups=[]),
        FakeSession(person),
        verifier=verifier,
        provider_name="authentik",
    )
    assert found is person


@pytest.mark.parametrize(
    "access",
    [False, None, "true", 1],
    ids=["access taken away", "no claim", "a string", "a number"],
)
async def test_without_authentik_saying_yes_it_is_403_without_a_challenge(
    provider, verifier, access
):
    # A refreshed token carries Authentik's answer as it is now, so this is
    # how someone whose Notes access was taken away is cut off (Authentik's
    # refresh grant does not re-check the application's policy). A token
    # without the claim fails closed.
    email_account = SimpleNamespace(id=uuid.uuid4(), is_active=True)
    with pytest.raises(HTTPException) as refused:
        await agent_user(
            token_for(provider, soloclef_notes_access=access),
            FakeSession(email_account),
            verifier=verifier,
            provider_name="authentik",
        )
    assert refused.value.status_code == 403
    assert not refused.value.headers


async def test_a_group_claim_without_access_does_not_help(provider, verifier):
    email_account = SimpleNamespace(id=uuid.uuid4(), is_active=True)
    with pytest.raises(HTTPException) as refused:
        await agent_user(
            token_for(provider, groups=["notes-users"], soloclef_notes_access=False),
            FakeSession(email_account),
            verifier=verifier,
            provider_name="authentik",
        )
    assert refused.value.status_code == 403


async def test_a_deactivated_account_is_refused_at_once(provider, verifier):
    gone = SimpleNamespace(id=uuid.uuid4(), is_active=False)
    with pytest.raises(HTTPException) as refused:
        await agent_user(
            token_for(provider),
            FakeSession(gone),
            verifier=verifier,
            provider_name="authentik",
        )
    assert refused.value.status_code == 403


def test_without_settings_no_token_is_an_agent_token(provider, monkeypatch):
    monkeypatch.setattr(principal, "_default_verifier", lambda: None)
    assert not is_agent_token(token_for(provider))


def test_with_settings_only_the_agent_issuer_is_an_agent_token(provider, verifier):
    assert is_agent_token(token_for(provider), verifier=verifier)
    assert not is_agent_token("ss_pat_abc", verifier=verifier)


def test_agent_principals_are_gated_like_api_keys():
    who = SimpleNamespace(id=uuid.uuid4())
    agent = AuthContext.agent(who)
    assert agent.is_gated
    assert not agent.is_session
    assert agent.method == "agent"


def test_settings_come_from_the_environment():
    settings = load_agent_sign_in_settings(
        {
            "MCP_OAUTH_ISSUER": " https://sso.example.com/application/o/soloclef-mcp/ ",
            "MCP_OAUTH_CLIENT_ID": "soloclef-mcp",
            "MCP_OAUTH_CLIENT_SECRET": "s3cret-value",
        }
    )
    assert settings.enabled
    assert settings.provider_name == "authentik"
    assert settings.discovery_url == (
        "https://sso.example.com/application/o/soloclef-mcp/"
        ".well-known/openid-configuration"
    )
    assert "s3cret-value" not in repr(settings)
    assert not load_agent_sign_in_settings({}).enabled
    assert not load_agent_sign_in_settings({"MCP_OAUTH_ISSUER": "x"}).enabled


def test_the_old_group_setting_is_ignored_with_a_warning(caplog):
    with caplog.at_level("WARNING", logger="app.agent_sign_in.settings"):
        settings = load_agent_sign_in_settings(
            {
                "MCP_OAUTH_ISSUER": "https://sso.example.com/application/o/soloclef-mcp/",
                "MCP_OAUTH_CLIENT_ID": "soloclef-mcp",
                "MCP_OAUTH_REQUIRED_GROUP": "notes-users",
            }
        )
    assert settings.enabled
    assert not hasattr(settings, "required_group")
    assert "MCP_OAUTH_REQUIRED_GROUP is no longer read" in caplog.text


def test_no_warning_without_the_old_setting(caplog):
    with caplog.at_level("WARNING", logger="app.agent_sign_in.settings"):
        load_agent_sign_in_settings({"MCP_OAUTH_REQUIRED_GROUP": " "})
    assert "MCP_OAUTH_REQUIRED_GROUP" not in caplog.text
