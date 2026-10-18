"""Only the provider's own id_token, for Notes, for this sign-in, is believed."""

from __future__ import annotations

import time

import jwt
import pytest

from app.auth_oidc.discovery import ProviderDirectory
from app.auth_oidc.errors import INVALID_ID_TOKEN, OidcLoginError
from app.auth_oidc.id_token import verify_id_token
from app.auth_oidc.login_attempt import LoginAttempt
from app.auth_oidc.settings import OidcSettings
from tests.utils.fake_oidc_provider import (
    CLIENT_ID,
    DISCOVERY_URL,
    REDIRECT_URI,
    FakeOidcProvider,
)

pytestmark = pytest.mark.unit

SETTINGS = OidcSettings(
    enabled=True,
    discovery_url=DISCOVERY_URL,
    client_id=CLIENT_ID,
    redirect_uri=REDIRECT_URI,
)


@pytest.fixture
def provider():
    return FakeOidcProvider()


@pytest.fixture
def directory(provider):
    return ProviderDirectory(DISCOVERY_URL, transport=provider.transport)


@pytest.fixture
def attempt():
    return LoginAttempt.start()


async def test_a_genuine_token_gives_the_identity(provider, directory, attempt):
    token = provider.sign(provider.standard_claims(attempt.nonce, email="A@Ex.com"))
    identity = await verify_id_token(token, SETTINGS, directory, attempt)
    assert identity.subject == "subject-1"
    assert identity.email == "a@ex.com"
    assert identity.email_verified is True
    assert identity.name == "A Person"


@pytest.mark.parametrize(
    "change",
    [
        {"iss": "https://sso.test/application/o/other-app/"},
        {"aud": "some-other-client"},
        {"exp": int(time.time()) - 3600},
        {"nonce": "a-nonce-from-another-sign-in"},
        {"aud": [CLIENT_ID, "another"], "azp": "another"},
    ],
    ids=["wrong issuer", "wrong audience", "expired", "wrong nonce", "azp"],
)
async def test_a_tampered_token_is_refused(provider, directory, attempt, change):
    token = provider.sign(provider.standard_claims(attempt.nonce, **change))
    with pytest.raises(OidcLoginError) as refused:
        await verify_id_token(token, SETTINGS, directory, attempt)
    assert refused.value.code == INVALID_ID_TOKEN


async def test_a_missing_nonce_is_refused(provider, directory, attempt):
    claims = provider.standard_claims(attempt.nonce)
    del claims["nonce"]
    with pytest.raises(OidcLoginError):
        await verify_id_token(provider.sign(claims), SETTINGS, directory, attempt)


@pytest.mark.parametrize("algorithm", ["HS256", "none"])
async def test_symmetric_and_unsigned_tokens_are_refused(
    provider, directory, attempt, algorithm
):
    claims = provider.standard_claims(attempt.nonce)
    key = "" if algorithm == "none" else "the-client-secret-as-hmac-key"
    token = jwt.encode(claims, key or None, algorithm=algorithm)
    with pytest.raises(OidcLoginError) as refused:
        await verify_id_token(token, SETTINGS, directory, attempt)
    assert refused.value.code == INVALID_ID_TOKEN


async def test_a_token_signed_by_someone_else_is_refused(directory, attempt):
    impostor = FakeOidcProvider()  # same issuer and kid, its own key
    token = impostor.sign(impostor.standard_claims(attempt.nonce))
    with pytest.raises(OidcLoginError) as refused:
        await verify_id_token(token, SETTINGS, directory, attempt)
    assert refused.value.code == INVALID_ID_TOKEN


async def test_a_rotated_key_is_fetched_once_seen(provider, directory, attempt):
    first = provider.sign(provider.standard_claims(attempt.nonce))
    await verify_id_token(first, SETTINGS, directory, attempt)
    provider.rotate_key()
    second = provider.sign(provider.standard_claims(attempt.nonce))
    identity = await verify_id_token(second, SETTINGS, directory, attempt)
    assert identity.subject == "subject-1"
    assert provider.jwks_requests == 2


async def test_email_verified_must_be_literally_true(provider, directory, attempt):
    token = provider.sign(
        provider.standard_claims(attempt.nonce, email_verified="true")
    )
    identity = await verify_id_token(token, SETTINGS, directory, attempt)
    assert identity.email_verified is False
