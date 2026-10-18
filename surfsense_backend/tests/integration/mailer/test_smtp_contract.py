"""Live SMTP contract; opt in with SMTP_INTEGRATION=1.

Every other mailer test stubs ``_send_blocking``, so the socket, the SMTP
conversation and the MIME round trip are otherwise never executed -- the code
most likely to be wrong in production is the code with no coverage. This test
sends through the real path and asks the receiving server what arrived.

Run it against the dev-stack Mailpit:

    docker compose -f docker/docker-compose.dev.yml --profile mail up -d mailpit
    SMTP_INTEGRATION=1 pytest tests/integration/mailer

What it still does not cover: STARTTLS negotiation and ``login()`` (Mailpit
here accepts plaintext without credentials), and deliverability -- a verified
From domain and spam placement are answerable only by a real provider.
"""

from __future__ import annotations

import os

import httpx
import pytest

from app.config import config
from app.mailer.protocol import Attachment, OutboundEmail
from app.mailer.smtp import SmtpMailer

pytestmark = pytest.mark.integration

MAILPIT_API = os.getenv("MAILPIT_API_URL", "http://localhost:8025")

pytest.importorskip("httpx")

if os.getenv("SMTP_INTEGRATION") != "1":
    pytest.skip(
        "Live SMTP contract; set SMTP_INTEGRATION=1 with Mailpit running",
        allow_module_level=True,
    )


@pytest.fixture
def mailpit(monkeypatch):
    """Point the sender at Mailpit and hand back a cleared inbox."""
    monkeypatch.setattr(config, "SMTP_ENABLED", True)
    monkeypatch.setattr(config, "SMTP_HOST", os.getenv("SMTP_HOST", "localhost"))
    monkeypatch.setattr(config, "SMTP_PORT", int(os.getenv("SMTP_PORT", "1025")))
    monkeypatch.setattr(config, "SMTP_USERNAME", "")
    monkeypatch.setattr(config, "SMTP_PASSWORD", "")
    monkeypatch.setattr(config, "SMTP_SECURITY", "none")
    monkeypatch.setattr(config, "SMTP_TIMEOUT_SECONDS", 10)
    monkeypatch.setattr(config, "SMTP_FROM", "noreply@surfsense.test")

    with httpx.Client(base_url=MAILPIT_API, timeout=10.0) as client:
        client.delete("/api/v1/messages")
        yield client


def _latest(client: httpx.Client) -> dict:
    messages = client.get("/api/v1/messages").raise_for_status().json()["messages"]
    assert messages, "Mailpit received nothing"
    return client.get(f"/api/v1/message/{messages[0]['ID']}").raise_for_status().json()


async def test_an_email_survives_a_real_smtp_conversation(mailpit):
    """The whole point: this is the only test that opens a socket."""
    await SmtpMailer().send(
        OutboundEmail(
            to="person@example.com",
            subject="Reset your password",
            text_body="Use the link to set a new password.",
        )
    )

    message = _latest(mailpit)
    assert message["From"]["Address"] == "noreply@surfsense.test"
    assert [r["Address"] for r in message["To"]] == ["person@example.com"]
    assert message["Subject"] == "Reset your password"


async def test_an_attachment_arrives_byte_for_byte(mailpit):
    """Base64 transfer encoding must not mangle an attachment."""
    content = b"-----BEGIN FILE-----\nCONTRACT-BYTES\n-----END FILE-----\n"

    await SmtpMailer().send(
        OutboundEmail(
            to="person@example.com",
            subject="Your export",
            text_body="Attached.",
            attachments=(Attachment(filename="export.txt", content=content),),
        )
    )

    message = _latest(mailpit)
    attachment = message["Attachments"][0]
    assert attachment["FileName"] == "export.txt"
    assert attachment["ContentType"] == "application/octet-stream"

    body = (
        mailpit.get(f"/api/v1/message/{message['ID']}/part/{attachment['PartID']}")
        .raise_for_status()
        .content
    )
    assert body == content


async def test_a_per_message_sender_reaches_the_wire(mailpit):
    """Sender travels on the message, so a shared connection stays shared."""
    await SmtpMailer().send(
        OutboundEmail(
            to="person@example.com",
            subject="Billing receipt",
            text_body="Thanks.",
            sender="billing@surfsense.test",
            reply_to="support@surfsense.test",
        )
    )

    message = _latest(mailpit)
    assert message["From"]["Address"] == "billing@surfsense.test"
    assert [r["Address"] for r in message["ReplyTo"]] == ["support@surfsense.test"]
