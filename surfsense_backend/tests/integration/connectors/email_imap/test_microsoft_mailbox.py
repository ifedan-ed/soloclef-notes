"""A connected Microsoft mailbox, end to end through the database.

Saved on the owner's connector row like any mailbox, signed in to with
XOAUTH2 (a loopback server answering like Exchange Online), its access token
refreshed at a stand-in for Microsoft's token endpoint and the new tokens
kept on the row; a grant Microsoft refuses shows as "needs reconnect" until
the mailbox is connected again.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from sqlalchemy import select

from app.connectors.email_imap import client as imap_client, service
from app.connectors.email_imap.microsoft import imap_sign_in
from app.connectors.email_imap.microsoft.account_record import (
    stored_access_token,
    stored_refresh_token,
)
from app.connectors.email_imap.microsoft.mailbox_identity import Mailbox
from app.connectors.email_imap.microsoft.mailbox_store import MicrosoftMailboxStore
from app.connectors.email_imap.microsoft.settings import MicrosoftMailSettings
from app.connectors.email_imap.microsoft.token_endpoint import (
    TokenGrant,
    refresh_grant,
)
from app.db import SearchSourceConnector, SearchSourceConnectorType
from tests.unit.connectors.email_imap.fake_xoauth2_imap_server import (
    FakeXoauth2ImapServer,
)
from tests.unit.connectors.email_imap.microsoft.fake_microsoft_login import (
    CLIENT_ID,
    CLIENT_SECRET,
    MAILBOX,
    FakeMicrosoftLogin,
)

pytestmark = pytest.mark.integration

SECRET = "test-secret-key-that-is-32-bytes-or-more"
SETTINGS = MicrosoftMailSettings(
    client_id=CLIENT_ID,
    client_secret=CLIENT_SECRET,
    redirect_uri="http://test/api/v1/auth/microsoft/mail/connector/callback",
    frontend_url="http://test",
    secret_key=SECRET,
)


@pytest.fixture
def login():
    return FakeMicrosoftLogin()


@pytest.fixture
def exchange(monkeypatch, login):
    """Exchange Online on loopback (no TLS), accepting the next refreshed token."""
    server = FakeXoauth2ImapServer(accepted_user=MAILBOX, accepted_token="access-1")
    monkeypatch.setattr(imap_sign_in, "IMAP_HOST", "127.0.0.1")
    monkeypatch.setattr(imap_sign_in, "IMAP_PORT", server.port)
    monkeypatch.setattr(imap_client, "_PinnedIMAP4_SSL", imap_client._PinnedIMAP4)
    monkeypatch.setattr(imap_sign_in, "current_settings", lambda: SETTINGS)

    async def refresh_at_the_fake(settings, refresh_token, *, transport=None, now=None):
        # Always the fake: a test must never reach login.microsoftonline.com.
        return await refresh_grant(
            settings, refresh_token, transport=login.transport, now=now
        )

    monkeypatch.setattr(imap_sign_in, "refresh_grant", refresh_at_the_fake)
    yield server
    server.close()


def _grant(*, refresh_token: str, expires_in: timedelta) -> TokenGrant:
    return TokenGrant(
        access_token="stale-access",
        refresh_token=refresh_token,
        expires_at=datetime.now(UTC) + expires_in,
        scopes=frozenset(),
    )


async def _connect(db_session, owner, *, refresh_token: str) -> dict:
    return await MicrosoftMailboxStore(db_session).save(
        owner.user.id,
        Mailbox(address=MAILBOX, subject="tid-1:subject-1"),
        _grant(refresh_token=refresh_token, expires_in=-timedelta(minutes=1)),
        secret_key=SECRET,
    )


async def _stored_account(db_session, owner) -> dict:
    connector = await db_session.scalar(
        select(SearchSourceConnector)
        .where(
            SearchSourceConnector.user_id == owner.user.id,
            SearchSourceConnector.connector_type
            == SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR,
        )
        .execution_options(populate_existing=True)
    )
    [account] = connector.config["accounts"]
    return account


async def test_a_stale_token_is_refreshed_kept_on_the_row_and_signs_in(
    db_session, make_owner, exchange, login
):
    owner = await make_owner()
    account = await _connect(db_session, owner, refresh_token="refresh-1")

    first = await service.test_connection(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
    )
    second = await service.test_connection(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
    )

    assert first == {"status": "ok", "folder_count": 2}
    assert second == first
    assert len(login.requests) == 1, (
        "the refreshed token is reused, not refreshed again"
    )
    stored = await _stored_account(db_session, owner)
    assert stored_access_token(stored, secret_key=SECRET)[0] == "access-1"
    assert stored_refresh_token(stored, secret_key=SECRET) == "refresh-2"
    assert exchange.sign_ins[0] == f"user={MAILBOX}\x01auth=Bearer access-1\x01\x01"


async def test_a_refused_grant_asks_for_reconnect_until_connected_again(
    db_session, make_owner, exchange, login
):
    owner = await make_owner()
    account = await _connect(db_session, owner, refresh_token="revoked")

    result = await service.test_connection(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
    )
    searched = await service.search_across_accounts(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=None,
        folder="INBOX",
        text=None,
        from_=None,
        to=None,
        subject=None,
        since=None,
        before=None,
        limit=10,
    )
    [listed] = await service.list_accounts(
        db_session, workspace_id=owner.workspace.id, user_id=owner.user.id
    )

    assert result == {"status": "error", "message": imap_sign_in.RECONNECT_MESSAGE}
    assert searched == [
        {
            "account_id": account["id"],
            "account_label": MAILBOX,
            "error": imap_sign_in.RECONNECT_MESSAGE,
        }
    ]
    assert listed["needs_reconnect"] is True
    assert len(login.requests) == 1, "a mailbox marked for reconnect is not retried"

    await MicrosoftMailboxStore(db_session).save(
        owner.user.id,
        Mailbox(address=MAILBOX.upper(), subject="tid-1:subject-1"),
        _grant(refresh_token="refresh-1", expires_in=timedelta(hours=1)),
        secret_key=SECRET,
    )
    [listed] = await service.list_accounts(
        db_session, workspace_id=owner.workspace.id, user_id=owner.user.id
    )
    assert listed["id"] == account["id"]
    assert listed["needs_reconnect"] is False


async def test_only_the_owner_can_look_up_a_mailbox_to_reconnect(
    db_session, make_owner, exchange
):
    owner, other = await make_owner(), await make_owner()
    account = await _connect(db_session, owner, refresh_token="refresh-1")
    store = MicrosoftMailboxStore(db_session)

    assert await store.address_of(owner.user.id, account["id"]) == MAILBOX
    assert await store.address_of(other.user.id, account["id"]) is None
