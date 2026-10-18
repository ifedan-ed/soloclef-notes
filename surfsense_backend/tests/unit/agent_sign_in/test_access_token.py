"""Only Authentik's own, current access token for the agent client is believed."""

from __future__ import annotations

import time

import httpx
import jwt
import pytest

from app.agent_sign_in.access_token import (
    KEY_REFRESH_COOLDOWN_SECONDS,
    NOTES_ACCESS_CLAIM,
    AgentProviderUnavailableError,
    AgentTokenRefusedError,
    AgentTokenVerifier,
)
from app.agent_sign_in.settings import AgentSignInSettings
from app.auth_oidc.discovery import ProviderDirectory
from tests.utils.fake_oidc_provider import FakeOidcProvider

pytestmark = pytest.mark.unit

ISSUER = "https://sso.test/application/o/soloclef-mcp/"
CLIENT_ID = "soloclef-mcp"
SETTINGS = AgentSignInSettings(issuer=ISSUER, client_id=CLIENT_ID)


def access_token_claims(**changes) -> dict:
    """Shaped like Authentik's access token (IDToken.to_access_token)."""
    now = int(time.time())
    claims = {
        "iss": ISSUER,
        "sub": "hashed-subject-1",
        "aud": CLIENT_ID,
        "azp": CLIENT_ID,
        "exp": now + 3600,
        "iat": now,
        "auth_time": now,
        "acr": "goauthentik.io/providers/oauth2/default",
        "jti": "jti-1",
        "uid": "uid-1",
        "scope": "openid email profile offline_access",
        "email": "operator@example.com",
        "email_verified": True,
        "name": "Operator",
    }
    claims.update(changes)
    return {k: v for k, v in claims.items() if v is not None}


class Clock:
    def __init__(self) -> None:
        self.now = 1000.0

    def __call__(self) -> float:
        return self.now


@pytest.fixture
def provider():
    return FakeOidcProvider(issuer=ISSUER, client_id=CLIENT_ID)


@pytest.fixture
def clock():
    return Clock()


@pytest.fixture
def verifier(provider, clock):
    directory = ProviderDirectory(SETTINGS.discovery_url, transport=provider.transport)
    return AgentTokenVerifier(SETTINGS, directory, clock=clock)


async def test_a_good_token_gives_the_identity(provider, verifier):
    identity = await verifier.verify(provider.sign(access_token_claims()))
    assert identity.subject == "hashed-subject-1"
    assert identity.email == "operator@example.com"
    assert identity.email_verified is True


@pytest.mark.parametrize(
    "change",
    [
        {"exp": int(time.time()) - 3600},
        {"aud": "soloclef-notes", "azp": "soloclef-notes"},
        {"aud": [CLIENT_ID, "soloclef-notes"], "azp": "soloclef-notes"},
        {"azp": "soloclef-notes"},
        {"iss": "https://sso.test/application/o/notes/"},
        {"sub": None},
    ],
    ids=[
        "expired",
        "wrong audience (the web sign-in's token)",
        "two audiences, azp another client",
        "issued to another client",
        "wrong issuer",
        "no subject",
    ],
)
async def test_a_wrong_token_is_refused(provider, verifier, change):
    token = provider.sign(access_token_claims(**change))
    with pytest.raises(AgentTokenRefusedError):
        await verifier.verify(token)


@pytest.mark.parametrize("algorithm", ["HS256", "none"])
async def test_symmetric_and_unsigned_tokens_are_refused(verifier, algorithm):
    key = None if algorithm == "none" else "the-client-secret-as-hmac-key"
    token = jwt.encode(access_token_claims(), key, algorithm=algorithm)
    with pytest.raises(AgentTokenRefusedError):
        await verifier.verify(token)


async def test_a_token_signed_by_someone_else_is_refused(verifier):
    impostor = FakeOidcProvider(issuer=ISSUER, client_id=CLIENT_ID)  # same kid
    with pytest.raises(AgentTokenRefusedError):
        await verifier.verify(impostor.sign(access_token_claims()))


async def test_garbage_is_refused(verifier):
    with pytest.raises(AgentTokenRefusedError):
        await verifier.verify("not-a-jwt")


@pytest.mark.parametrize("access", [True, False])
async def test_the_notes_access_claim_is_read(provider, verifier, access):
    token = provider.sign(
        access_token_claims(**{NOTES_ACCESS_CLAIM: access, "groups": ["notes-users"]})
    )
    assert (await verifier.verify(token)).notes_access is access


@pytest.mark.parametrize(
    "access",
    [None, "true", 1, [True]],
    ids=["absent", "a string", "a number", "a list"],
)
async def test_no_usable_notes_access_claim_is_none(provider, verifier, access):
    token = provider.sign(access_token_claims(**{NOTES_ACCESS_CLAIM: access}))
    assert (await verifier.verify(token)).notes_access is None


async def test_unverified_email_is_reported_as_unverified(provider, verifier):
    token = provider.sign(access_token_claims(email_verified="true"))
    assert (await verifier.verify(token)).email_verified is False


async def test_a_rotated_key_is_fetched_once_seen(provider, verifier):
    await verifier.verify(provider.sign(access_token_claims()))
    provider.rotate_key()
    identity = await verifier.verify(provider.sign(access_token_claims()))
    assert identity.subject == "hashed-subject-1"
    assert provider.jwks_requests == 2


async def test_unknown_key_ids_refetch_keys_at_most_once_a_minute(
    provider, verifier, clock
):
    await verifier.verify(provider.sign(access_token_claims()))
    for n in range(5):
        forged = jwt.encode(
            access_token_claims(),
            provider._private,
            algorithm="RS256",
            headers={"kid": f"made-up-{n}"},
        )
        with pytest.raises(AgentTokenRefusedError):
            await verifier.verify(forged)
    assert provider.jwks_requests == 2  # the first read and one forced refresh

    clock.now += KEY_REFRESH_COOLDOWN_SECONDS + 1
    with pytest.raises(AgentTokenRefusedError):
        await verifier.verify(
            jwt.encode(
                access_token_claims(),
                provider._private,
                algorithm="RS256",
                headers={"kid": "made-up-later"},
            )
        )
    assert provider.jwks_requests == 3


class Outage:
    """The provider's transport, with sso.example.com going down on demand."""

    def __init__(self, provider) -> None:
        self.provider = provider
        self.down: str | None = None  # None, "discovery" or "keys"

    def _handle(self, request: httpx.Request) -> httpx.Response:
        url = str(request.url)
        if self.down == "discovery" and url.endswith("openid-configuration"):
            return httpx.Response(502, text="Bad Gateway")
        if self.down == "keys" and url.endswith("/jwks/"):
            raise httpx.ConnectError("connection refused", request=request)
        return self.provider._handle(request)

    @property
    def transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self._handle)


async def test_an_unreachable_provider_is_unavailable_not_a_bad_token(provider, clock):
    outage = Outage(provider)
    outage.down = "discovery"
    directory = ProviderDirectory(SETTINGS.discovery_url, transport=outage.transport)
    verifier = AgentTokenVerifier(SETTINGS, directory, clock=clock)
    with pytest.raises(AgentProviderUnavailableError):
        await verifier.verify(provider.sign(access_token_claims()))


async def test_unreadable_keys_are_unavailable_not_a_bad_token(provider, clock):
    outage = Outage(provider)
    outage.down = "keys"
    directory = ProviderDirectory(SETTINGS.discovery_url, transport=outage.transport)
    verifier = AgentTokenVerifier(SETTINGS, directory, clock=clock)
    with pytest.raises(AgentProviderUnavailableError):
        await verifier.verify(provider.sign(access_token_claims()))


async def test_an_empty_key_set_is_unavailable(provider, verifier, monkeypatch):
    async def no_keys(*, refresh=False):
        return {"keys": []}

    monkeypatch.setattr(verifier._directory, "signing_keys", no_keys)
    with pytest.raises(AgentProviderUnavailableError):
        await verifier.verify(provider.sign(access_token_claims()))


def test_only_tokens_claiming_the_agent_issuer_take_this_path(provider, verifier):
    assert verifier.claims_this_issuer(provider.sign(access_token_claims()))
    no_slash = provider.sign(access_token_claims(iss=ISSUER.rstrip("/")))
    assert verifier.claims_this_issuer(no_slash)
    other = provider.sign(
        access_token_claims(iss="https://sso.test/application/o/notes/")
    )
    assert not verifier.claims_this_issuer(other)
    assert not verifier.claims_this_issuer("ss_pat_abc")
    session_jwt = jwt.encode({"sub": "user-id", "aud": ["fastapi-users:auth"]}, "s")
    assert not verifier.claims_this_issuer(session_jwt)
