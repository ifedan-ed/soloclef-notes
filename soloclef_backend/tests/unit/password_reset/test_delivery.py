"""Queueing the reset email: capped per address, expires with the link, never logs the token."""

from __future__ import annotations

import time

import pytest

from app.config import config
from app.mail_queue import MailQueueUnavailableError
from app.password_reset import delivery

pytestmark = pytest.mark.unit


class _Outbox:
    """Stands in for the mail queue."""

    def __init__(self, fail: bool = False):
        self.sent = []
        self.not_after = []
        self.fail = fail

    async def enqueue(self, message, *, kind, ref=None, not_after=None):
        if self.fail:
            raise MailQueueUnavailableError("ConnectionError")
        assert kind == "password_reset"
        self.sent.append(message)
        self.not_after.append(not_after)
        return "mail-1"


@pytest.fixture
def outbox(monkeypatch):
    box = _Outbox()
    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: True)
    monkeypatch.setattr(delivery, "enqueue_email", box.enqueue)
    monkeypatch.setattr(delivery, "_within_address_cap", lambda email: True)
    return box


async def test_sends_one_email_to_the_account(outbox):
    assert await delivery.send_reset_email("u1", "a@example.com", "tok-secret-123")
    assert [m.to for m in outbox.sent] == ["a@example.com"]
    assert "tok-secret-123" in outbox.sent[0].text_body


async def test_the_email_expires_with_the_link(outbox):
    before = time.time()
    assert await delivery.send_reset_email("u1", "a@example.com", "tok")
    lifetime = config.PASSWORD_RESET_TOKEN_LIFETIME_SECONDS
    assert before + lifetime <= outbox.not_after[0] <= time.time() + lifetime


async def test_nothing_is_sent_when_mail_is_off(outbox, monkeypatch):
    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: False)
    assert not await delivery.send_reset_email("u1", "a@example.com", "tok")
    assert outbox.sent == []


async def test_an_address_over_its_hourly_cap_gets_nothing(outbox, monkeypatch):
    monkeypatch.setattr(delivery, "_within_address_cap", lambda email: False)
    assert not await delivery.send_reset_email("u1", "a@example.com", "tok")
    assert outbox.sent == []


async def test_the_token_never_reaches_the_log(outbox, caplog):
    caplog.set_level("DEBUG")
    outbox.fail = True
    assert not await delivery.send_reset_email("u1", "a@example.com", "tok-secret-123")
    outbox.fail = False
    assert await delivery.send_reset_email("u1", "a@example.com", "tok-secret-123")
    assert "tok-secret-123" not in caplog.text


def test_the_cap_counts_per_address_case_insensitively():
    assert delivery._address_key("A@Example.com ") == delivery._address_key(
        "a@example.com"
    )
    assert "a@example.com" not in delivery._address_key("a@example.com")


async def test_the_mail_is_identified_by_the_token_it_carries(outbox):
    assert await delivery.send_reset_email("u1", "a@example.com", "tok-one")
    assert await delivery.send_reset_email("u1", "a@example.com", "tok-one")
    assert await delivery.send_reset_email("u1", "a@example.com", "tok-two")
    first, again, second = (m.idempotency_key for m in outbox.sent)
    assert first == again
    assert first != second


async def test_the_token_is_not_in_the_key_that_identifies_the_mail(outbox):
    assert await delivery.send_reset_email("u1", "a@example.com", "tok-secret-123")
    key = outbox.sent[0].idempotency_key
    assert key.startswith("password_reset:")
    assert "tok-secret-123" not in key


async def test_two_accounts_asking_at_once_are_two_different_mails(outbox):
    assert await delivery.send_reset_email("u1", "a@example.com", "tok-a")
    assert await delivery.send_reset_email("u2", "b@example.com", "tok-b")
    assert outbox.sent[0].idempotency_key != outbox.sent[1].idempotency_key
