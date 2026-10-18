"""Sending the reset email: capped per address, never logs the token."""

from __future__ import annotations

import pytest

from app.mailer import MailerUnavailableError
from app.password_reset import delivery

pytestmark = pytest.mark.unit


class _Outbox:
    def __init__(self, fail: bool = False):
        self.sent = []
        self.fail = fail

    async def send(self, message):
        if self.fail:
            raise MailerUnavailableError("smtp down, token tok-secret-123 in text")
        self.sent.append(message)


@pytest.fixture
def outbox(monkeypatch):
    box = _Outbox()
    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: True)
    monkeypatch.setattr(delivery, "get_mailer", lambda: box)
    monkeypatch.setattr(delivery, "_within_address_cap", lambda email: True)
    return box


async def test_sends_one_email_to_the_account(outbox):
    assert await delivery.send_reset_email("u1", "a@example.com", "tok-secret-123")
    assert [m.to for m in outbox.sent] == ["a@example.com"]
    assert "tok-secret-123" in outbox.sent[0].text_body


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
