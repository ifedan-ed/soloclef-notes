"""Queueing the invitation email, and what makes one send occasion distinct.

A resend keeps the invite's code and bumps ``last_sent_at``, so the occasion
and not the invitation is what identifies the mail: a double-submitted resend
is one email, and a resend a minute later is a second one.
"""

from __future__ import annotations

from datetime import UTC, datetime

import pytest

from app.mailer.protocol import OutboundEmail
from app.workspace_invites import delivery

pytestmark = pytest.mark.unit

SENT_AT = datetime(2026, 11, 8, 9, 0, tzinfo=UTC)


class _Outbox:
    def __init__(self):
        self.sent = []

    async def enqueue(self, message, *, kind, ref=None, not_after=None):
        assert kind == "invite"
        self.sent.append(message)
        return f"mail-{len(self.sent)}"


@pytest.fixture
def outbox(monkeypatch):
    box = _Outbox()
    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: True)
    monkeypatch.setattr(delivery, "enqueue_email", box.enqueue)
    return box


def _message() -> OutboundEmail:
    return OutboundEmail(to="person@example.test", subject="Join", text_body="x")


async def test_one_send_occasion_is_one_mail(outbox):
    assert await delivery.send_invite_email(7, _message(), sent_at=SENT_AT)
    assert await delivery.send_invite_email(7, _message(), sent_at=SENT_AT)
    assert outbox.sent[0].idempotency_key == outbox.sent[1].idempotency_key


async def test_a_resend_is_a_second_mail(outbox):
    later = SENT_AT.replace(minute=5)
    assert await delivery.send_invite_email(7, _message(), sent_at=SENT_AT)
    assert await delivery.send_invite_email(7, _message(), sent_at=later)
    assert outbox.sent[0].idempotency_key != outbox.sent[1].idempotency_key


async def test_another_invite_sent_at_the_same_moment_is_its_own_mail(outbox):
    assert await delivery.send_invite_email(7, _message(), sent_at=SENT_AT)
    assert await delivery.send_invite_email(8, _message(), sent_at=SENT_AT)
    assert outbox.sent[0].idempotency_key != outbox.sent[1].idempotency_key


async def test_an_invite_with_no_recorded_occasion_is_still_queued(outbox):
    assert await delivery.send_invite_email(7, _message(), sent_at=None)
    assert outbox.sent[0].idempotency_key is None
