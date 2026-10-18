"""The URL Notes sends the browser to."""

from __future__ import annotations

from urllib.parse import parse_qs, urlsplit

import pytest

from app.auth_oidc.authorization_request import authorization_url
from app.auth_oidc.discovery import parse_metadata
from app.auth_oidc.login_attempt import LoginAttempt
from app.auth_oidc.settings import OidcSettings
from tests.utils.fake_oidc_provider import (
    CLIENT_ID,
    DISCOVERY_URL,
    REDIRECT_URI,
    FakeOidcProvider,
)

pytestmark = pytest.mark.unit


def test_asks_for_a_code_with_pkce_and_a_nonce():
    settings = OidcSettings(
        enabled=True,
        discovery_url=DISCOVERY_URL,
        client_id=CLIENT_ID,
        redirect_uri=REDIRECT_URI,
    )
    provider = parse_metadata(FakeOidcProvider()._discovery(), DISCOVERY_URL)
    attempt = LoginAttempt.start()

    url = authorization_url(settings, provider, attempt)

    parts = urlsplit(url)
    assert f"{parts.scheme}://{parts.netloc}{parts.path}" == (
        provider.authorization_endpoint
    )
    query = {k: v[0] for k, v in parse_qs(parts.query).items()}
    assert query == {
        "response_type": "code",
        "client_id": CLIENT_ID,
        "redirect_uri": REDIRECT_URI,
        "scope": "openid email profile",
        "state": attempt.state,
        "nonce": attempt.nonce,
        "code_challenge": attempt.code_challenge,
        "code_challenge_method": "S256",
    }
    assert attempt.code_verifier not in url
