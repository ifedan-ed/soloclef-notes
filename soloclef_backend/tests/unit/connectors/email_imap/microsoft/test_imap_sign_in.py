"""Signing in to a stored Microsoft mailbox: a fresh token, refreshed and saved as needed."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest

from app.connectors.email_imap import credentials as creds
from app.connectors.email_imap.client import ImapConnectionError
from app.connectors.email_imap.microsoft.account_record import (
    new_mailbox_record,
    stored_access_token,
    stored_refresh_token,
)
from app.connectors.email_imap.microsoft.imap_sign_in import (
    RECONNECT_MESSAGE,
    microsoft_imap_settings,
)
from app.connectors.email_imap.microsoft.mailbox_identity import Mailbox
from app.connectors.email_imap.microsoft.settings import (
    IMAP_HOST,
    IMAP_PORT,
    MicrosoftMailSettings,
)
from app.connectors.email_imap.microsoft.token_endpoint import TokenGrant
from tests.unit.connectors.email_imap.microsoft.fake_microsoft_login import (
    CLIENT_ID,
    CLIENT_SECRET,
    MAILBOX,
    FakeMicrosoftLogin,
)

pytestmark = pytest.mark.unit

SECRET = "test-secret-key-that-is-32-bytes-or-more"
SETTINGS = MicrosoftMailSettings(
    client_id=CLIENT_ID,
    client_secret=CLIENT_SECRET,
    redirect_uri="https://notes.example/api/v1/auth/microsoft/mail/connector/callback",
    frontend_url="https://notes.example",
    secret_key=SECRET,
)
NOW = datetime(2026, 10, 9, 12, 0, tzinfo=UTC)


def _account(*, expires_in: timedelta, refresh_token: str = "refresh-1") -> dict:
    grant = TokenGrant(
        access_token="stored-access",
        refresh_token=refresh_token,
        expires_at=NOW + expires_in,
        scopes=frozenset(),
    )
    return new_mailbox_record(
        Mailbox(address=MAILBOX, subject="tid-1:subject-1"),
        grant,
        secret_key=SECRET,
        now=NOW,
    )


class Saved(list):
    async def __call__(self, fields: dict) -> None:
        self.append(fields)


async def _sign_in(account, login, saved):
    return await microsoft_imap_settings(
        account, save=saved, settings=SETTINGS, transport=login.transport, now=NOW
    )


def test_a_new_record_keeps_only_ciphertext_and_reads_like_any_account():
    account = _account(expires_in=timedelta(hours=1))
    assert account["_encrypted"] is True
    assert "stored-access" not in str(account)
    assert "refresh-1" not in str(account)
    assert creds.public_account(account) == {
        "id": account["id"],
        "label": MAILBOX,
        "host": IMAP_HOST,
        "port": IMAP_PORT,
        "use_tls": True,
        "username": MAILBOX,
        "created_at": NOW.isoformat(),
        "auth": "microsoft",
        "needs_reconnect": False,
    }


async def test_a_token_with_time_left_is_used_as_is():
    login, saved = FakeMicrosoftLogin(), Saved()

    settings = await _sign_in(_account(expires_in=timedelta(minutes=30)), login, saved)

    assert settings.oauth_access_token == "stored-access"
    assert (settings.host, settings.port, settings.use_tls) == (IMAP_HOST, 993, True)
    assert settings.username == MAILBOX
    assert login.requests == []
    assert saved == []


async def test_a_token_about_to_lapse_is_refreshed_and_the_new_tokens_saved():
    login, saved = FakeMicrosoftLogin(), Saved()
    account = _account(expires_in=timedelta(minutes=2))

    settings = await _sign_in(account, login, saved)

    assert settings.oauth_access_token == "access-1"
    assert login.requests[0]["refresh_token"] == "refresh-1"
    [fields] = saved
    after = {**account, **fields}
    assert stored_access_token(after, secret_key=SECRET) == (
        "access-1",
        NOW + timedelta(seconds=3599),
    )
    assert stored_refresh_token(after, secret_key=SECRET) == "refresh-2"
    assert "access-1" not in str(fields)


async def test_a_host_written_into_the_record_never_receives_the_token():
    login, saved = FakeMicrosoftLogin(), Saved()
    account = {**_account(expires_in=timedelta(hours=1)), "host": "evil.example"}
    settings = await _sign_in(account, login, saved)
    assert settings.host == IMAP_HOST


async def test_a_revoked_grant_is_marked_for_reconnecting_and_says_so():
    login, saved = FakeMicrosoftLogin(refresh_tokens=set()), Saved()

    with pytest.raises(ImapConnectionError) as refused:
        await _sign_in(_account(expires_in=timedelta(0)), login, saved)

    assert str(refused.value) == RECONNECT_MESSAGE
    assert saved == [{"needs_reconnect": True}]


async def test_a_mailbox_marked_for_reconnecting_does_not_ask_microsoft_again():
    login, saved = FakeMicrosoftLogin(), Saved()
    account = {**_account(expires_in=timedelta(0)), "needs_reconnect": True}

    with pytest.raises(ImapConnectionError):
        await _sign_in(account, login, saved)

    assert login.requests == []


async def test_microsoft_being_down_leaves_the_mailbox_connected():
    login = FakeMicrosoftLogin(scripted=[(503, {"error": "temporarily_unavailable"})])
    saved = Saved()

    with pytest.raises(ImapConnectionError) as failed:
        await _sign_in(_account(expires_in=timedelta(0)), login, saved)

    assert str(failed.value) != RECONNECT_MESSAGE
    assert saved == []


async def test_an_expired_client_secret_points_at_the_server_not_the_mailbox():
    login = FakeMicrosoftLogin(scripted=[(401, {"error": "invalid_client"})])
    saved = Saved()

    with pytest.raises(ImapConnectionError) as failed:
        await _sign_in(_account(expires_in=timedelta(0)), login, saved)

    assert "administrator" in str(failed.value)
    assert saved == []


async def test_tokens_that_no_longer_decrypt_need_a_reconnect():
    login, saved = FakeMicrosoftLogin(), Saved()
    account = {**_account(expires_in=timedelta(0)), "refresh_token": "garbage"}

    with pytest.raises(ImapConnectionError) as refused:
        await _sign_in(account, login, saved)

    assert str(refused.value) == RECONNECT_MESSAGE
    assert saved == [{"needs_reconnect": True}]
    assert login.requests == []


async def test_without_settings_it_says_microsoft_is_not_set_up():
    login, saved = FakeMicrosoftLogin(), Saved()
    with pytest.raises(ImapConnectionError) as off:
        await microsoft_imap_settings(
            _account(expires_in=timedelta(hours=1)),
            save=saved,
            settings=MicrosoftMailSettings(),
            transport=login.transport,
            now=NOW,
        )
    assert "set up" in str(off.value)
