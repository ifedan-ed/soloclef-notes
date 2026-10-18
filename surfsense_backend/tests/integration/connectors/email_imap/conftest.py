"""Fixtures for Email (IMAP) integration tests: a real GreenMail IMAP server.

GreenMail runs as a docker container alongside the test DB — see
``custom/docs/email-imap-connector.md`` for how to start it locally
(``docker run greenmail/standalone`` with SMTP+IMAP(S) ports and two seeded
users). These tests talk real IMAP/SMTP wire protocol to it; nothing about
our own IMAP client is mocked.
"""

from __future__ import annotations

import os
import smtplib
import ssl
import uuid
from email.message import EmailMessage
from types import SimpleNamespace

import pytest
import pytest_asyncio

from app.auth.context import AuthContext
from app.db import User, Workspace
from app.routes.workspaces_routes import create_default_roles_and_membership

GREENMAIL_HOST = os.environ.get("GREENMAIL_HOST", "127.0.0.1")
GREENMAIL_SMTP_PORT = int(os.environ.get("GREENMAIL_SMTP_PORT", "53025"))
GREENMAIL_IMAPS_PORT = int(os.environ.get("GREENMAIL_IMAPS_PORT", "53993"))

ALICE = SimpleNamespace(username="alice", password="alicepass", email="alice@example.test")
BOB = SimpleNamespace(username="bob", password="bobpass", email="bob@example.test")


def _greenmail_reachable() -> bool:
    import socket

    try:
        with socket.create_connection((GREENMAIL_HOST, GREENMAIL_IMAPS_PORT), timeout=1):
            return True
    except OSError:
        return False


requires_greenmail = pytest.mark.skipif(
    not _greenmail_reachable(),
    reason=(
        "GreenMail not reachable on "
        f"{GREENMAIL_HOST}:{GREENMAIL_IMAPS_PORT} — start it with "
        "`docker run -p 53025:3025 -p 53993:3993 "
        '-e GREENMAIL_OPTS="-Dgreenmail.smtp.hostname=0.0.0.0 -Dgreenmail.smtp.port=3025 '
        "-Dgreenmail.imaps.hostname=0.0.0.0 -Dgreenmail.imaps.port=3993 "
        '-Dgreenmail.users=alice:alicepass@example.test,bob:bobpass@example.test" '
        "greenmail/standalone`"
    ),
)


def deliver_message(
    to: str,
    *,
    subject: str,
    body: str,
    from_: str = "sender@example.test",
    attachment: tuple[str, bytes, str] | None = None,
) -> None:
    """Deliver a message via real SMTP. ``attachment`` is (filename, bytes, content_type)."""
    msg = EmailMessage()
    msg["From"] = from_
    msg["To"] = to
    msg["Subject"] = subject
    msg.set_content(body)
    if attachment is not None:
        filename, data, content_type = attachment
        maintype, _, subtype = content_type.partition("/")
        msg.add_attachment(
            data, maintype=maintype, subtype=subtype or "octet-stream", filename=filename
        )
    with smtplib.SMTP(GREENMAIL_HOST, GREENMAIL_SMTP_PORT, timeout=10) as s:
        s.send_message(msg)


def raw_imap_connection():
    """A second, independent IMAP connection — used to inspect server-side
    state (like the \\Seen flag) without going through our own client."""
    import imaplib

    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    return imaplib.IMAP4_SSL(GREENMAIL_HOST, GREENMAIL_IMAPS_PORT, ssl_context=ctx, timeout=10)


@pytest.fixture
def imap_settings():
    return {
        "host": GREENMAIL_HOST,
        "port": GREENMAIL_IMAPS_PORT,
        "use_tls": True,
    }


@pytest.fixture(autouse=True)
def _allow_the_local_test_server(monkeypatch):
    """The SSRF guard (``app.connectors.email_imap.credentials``,
    ``app.connectors.email_imap.client``, ``app.utils.crawl.net_guard``)
    correctly refuses a loopback/private host — that is the point of
    ``tests/unit/connectors/email_imap/test_client_ssrf_guard.py`` and the
    credentials unit tests — but GreenMail, this suite's real IMAP server,
    legitimately runs on ``127.0.0.1``. Both the save-time check
    (``new_account_record``) and the connect-time pin
    (``ReadOnlyImapClient.__enter__``) ultimately ask the same
    ``is_public_address`` whether an address is allowed, so patching it in
    both modules to always say yes is enough to let this suite reach its own
    test server. Everything downstream of that answer — resolution,
    pinning, quoting, actually connecting over the wire — still runs for
    real; only the "is this address private" verdict is stubbed, and only in
    this test process.
    """
    from app.connectors.email_imap import client as imap_client, credentials as creds

    monkeypatch.setattr(creds, "is_publicly_routable", lambda _url: True)
    monkeypatch.setattr(imap_client, "is_public_address", lambda _addr: True)


@pytest_asyncio.fixture
async def make_owner(db_session):
    async def build() -> SimpleNamespace:
        user = User(
            id=uuid.uuid4(),
            email=f"imap-{uuid.uuid4().hex[:8]}@surfsense.test",
            hashed_password="x",
            is_active=True,
            is_superuser=False,
            is_verified=True,
        )
        db_session.add(user)
        await db_session.flush()
        workspace = Workspace(name=f"Space {uuid.uuid4().hex[:6]}", user_id=user.id)
        db_session.add(workspace)
        await db_session.flush()
        await create_default_roles_and_membership(db_session, workspace.id, user.id)
        await db_session.flush()
        return SimpleNamespace(
            user=user, workspace=workspace, auth=AuthContext.session(user)
        )

    return build
