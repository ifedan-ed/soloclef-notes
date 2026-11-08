"""Signing in to a mailbox with an OAuth access token (SASL XOAUTH2).

Real imaplib over a real socket, against a loopback server that answers like
Exchange Online. Only TLS and the public-address check are taken out: the
server has no certificate and lives on 127.0.0.1.
"""

from __future__ import annotations

import pytest

from app.connectors.email_imap import client as imap_client
from app.connectors.email_imap.client import (
    ImapAccountSettings,
    ImapConnectionError,
    ReadOnlyImapClient,
)
from tests.unit.connectors.email_imap.fake_xoauth2_imap_server import (
    FakeXoauth2ImapServer,
)

pytestmark = pytest.mark.unit

MAILBOX = "someone@contoso.example"
TOKEN = "EwBAAl3BAAUFFpUAo7J3Ve0bjLBWZWCclRC3EoAA"


@pytest.fixture
def exchange(monkeypatch):
    server = FakeXoauth2ImapServer(accepted_user=MAILBOX, accepted_token=TOKEN)
    monkeypatch.setattr(imap_client, "is_public_address", lambda _addr: True)
    monkeypatch.setattr(imap_client, "_PinnedIMAP4_SSL", imap_client._PinnedIMAP4)
    yield server
    server.close()


def _settings(server: FakeXoauth2ImapServer, token: str) -> ImapAccountSettings:
    return ImapAccountSettings(
        host="127.0.0.1",
        port=server.port,
        use_tls=True,
        username=MAILBOX,
        password="",
        oauth_access_token=token,
    )


def test_an_access_token_signs_in_with_xoauth2_and_never_sends_a_password(exchange):
    with ReadOnlyImapClient(_settings(exchange, TOKEN)) as client:
        folders = client.list_folders()

    assert folders == ["INBOX", "Sent Items"]
    assert exchange.sign_ins == [f"user={MAILBOX}\x01auth=Bearer {TOKEN}\x01\x01"]
    assert not any(c.upper().startswith("LOGIN") for c in exchange.commands)


def test_a_refused_token_is_a_connection_error_that_says_access_was_refused(exchange):
    with (
        pytest.raises(ImapConnectionError) as refused,
        ReadOnlyImapClient(_settings(exchange, "expired-token")),
    ):
        pass

    assert "AUTHENTICATE failed" in str(refused.value)
    assert "refused" in str(refused.value).lower()
    assert "expired-token" not in str(refused.value)


def test_the_token_is_kept_out_of_the_settings_repr():
    settings = ImapAccountSettings(
        host="outlook.office365.com",
        port=993,
        use_tls=True,
        username=MAILBOX,
        password="app-password",
        oauth_access_token=TOKEN,
    )
    assert TOKEN not in repr(settings)
    assert "app-password" not in repr(settings)
