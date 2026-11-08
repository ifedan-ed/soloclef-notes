"""Trading a consent code for tokens, and refreshing them, at Microsoft."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import httpx
import pytest

from app.connectors.email_imap.microsoft.settings import (
    IMAP_SCOPE,
    MicrosoftMailSettings,
)
from app.connectors.email_imap.microsoft.token_endpoint import (
    GrantRevokedError,
    TokenEndpointError,
    redeem_code,
    refresh_grant,
)
from tests.unit.connectors.email_imap.microsoft.fake_microsoft_login import (
    CLIENT_ID,
    CLIENT_SECRET,
    FakeMicrosoftLogin,
)

pytestmark = pytest.mark.unit

SETTINGS = MicrosoftMailSettings(
    client_id=CLIENT_ID,
    client_secret=CLIENT_SECRET,
    redirect_uri="https://notes.example/api/v1/auth/microsoft/mail/connector/callback",
    frontend_url="https://notes.example",
    secret_key="test-secret-key-that-is-32-bytes-or-more",
)
NOW = datetime(2026, 10, 9, 12, 0, tzinfo=UTC)


async def test_a_code_is_redeemed_with_its_pkce_verifier_and_the_client_secret():
    login = FakeMicrosoftLogin()

    grant = await redeem_code(
        SETTINGS, "good-code", "verifier", transport=login.transport, now=NOW
    )

    sent = login.requests[0]
    assert sent["grant_type"] == "authorization_code"
    assert sent["code_verifier"] == "verifier"
    assert sent["redirect_uri"] == SETTINGS.redirect_uri
    assert IMAP_SCOPE in sent["scope"].split()
    assert grant.access_token == "access-1"
    assert grant.refresh_token == "refresh-2"
    assert grant.expires_at == NOW + timedelta(seconds=3599)
    assert grant.grants_imap
    assert grant.id_token


async def test_a_grant_without_the_imap_scope_says_so():
    login = FakeMicrosoftLogin(granted_scope="https://outlook.office.com/User.Read")
    grant = await redeem_code(
        SETTINGS, "good-code", "verifier", transport=login.transport, now=NOW
    )
    assert not grant.grants_imap


async def test_a_wrong_verifier_is_a_refused_grant():
    login = FakeMicrosoftLogin()
    with pytest.raises(GrantRevokedError):
        await redeem_code(SETTINGS, "good-code", "other", transport=login.transport)


async def test_a_refresh_returns_the_new_tokens():
    login = FakeMicrosoftLogin()
    grant = await refresh_grant(
        SETTINGS, "refresh-1", transport=login.transport, now=NOW
    )
    assert login.requests[0]["grant_type"] == "refresh_token"
    assert login.requests[0]["refresh_token"] == "refresh-1"
    assert (grant.access_token, grant.refresh_token) == ("access-1", "refresh-2")


@pytest.mark.parametrize("error", ["invalid_grant", "interaction_required"])
async def test_a_revoked_or_expired_refresh_token_means_reconnect(error):
    login = FakeMicrosoftLogin(scripted=[(400, {"error": error})])
    with pytest.raises(GrantRevokedError):
        await refresh_grant(SETTINGS, "refresh-1", transport=login.transport)


async def test_an_expired_client_secret_is_the_servers_problem_not_the_accounts():
    wrong = MicrosoftMailSettings(**{**SETTINGS.__dict__, "client_secret": "old"})
    login = FakeMicrosoftLogin()
    with pytest.raises(TokenEndpointError) as failed:
        await refresh_grant(wrong, "refresh-1", transport=login.transport)
    assert not isinstance(failed.value, GrantRevokedError)


async def test_microsoft_being_down_is_not_a_revoked_grant():
    def down(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("refused", request=request)

    with pytest.raises(TokenEndpointError) as failed:
        await refresh_grant(SETTINGS, "refresh-1", transport=httpx.MockTransport(down))
    assert not isinstance(failed.value, GrantRevokedError)

    login = FakeMicrosoftLogin(scripted=[(503, {"error": "temporarily_unavailable"})])
    with pytest.raises(TokenEndpointError) as failed:
        await refresh_grant(SETTINGS, "refresh-1", transport=login.transport)
    assert not isinstance(failed.value, GrantRevokedError)
