"""Sending an automation's run-result email: owner only, best-effort."""

from __future__ import annotations

import uuid
from types import SimpleNamespace

import pytest

from app.automations.notify import delivery

pytestmark = pytest.mark.unit


class _Outbox:
    def __init__(self):
        self.sent = []

    async def send(self, message):
        self.sent.append(message)


@pytest.fixture
def outbox(monkeypatch):
    box = _Outbox()
    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: True)
    monkeypatch.setattr(delivery, "get_mailer", lambda: box)
    return box


class _Session:
    def __init__(self, user):
        self._user = user

    async def get(self, _model, _id):
        return self._user


async def test_success_email_goes_to_the_owner_with_the_output(outbox):
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    await delivery.send_run_result_email(
        _Session(user),
        owner_user_id=user.id,
        automation_name="Weekly digest",
        status="succeeded",
        result={"final_message": "Here is your digest."},
    )
    assert len(outbox.sent) == 1
    message = outbox.sent[0]
    assert message.to == "owner@example.com"
    assert "Weekly digest" in message.subject
    assert "Here is your digest." in message.text_body


async def test_failure_email_carries_the_reason(outbox):
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    await delivery.send_run_result_email(
        _Session(user),
        owner_user_id=user.id,
        automation_name="Weekly digest",
        status="failed",
        error={"message": "model provider down"},
    )
    [message] = outbox.sent
    assert "failed" in message.subject
    assert "model provider down" in message.text_body


async def test_needs_attention_email_carries_the_reason(outbox):
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    await delivery.send_run_result_email(
        _Session(user),
        owner_user_id=user.id,
        automation_name="Weekly digest",
        status="needs_attention",
        error={"message": "needs a human decision"},
    )
    [message] = outbox.sent
    assert "needs attention" in message.subject
    assert "needs a human decision" in message.text_body


async def test_nothing_is_sent_when_mail_is_off(outbox, monkeypatch):
    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: False)
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    await delivery.send_run_result_email(
        _Session(user),
        owner_user_id=user.id,
        automation_name="Weekly digest",
        status="succeeded",
        result={"final_message": "x"},
    )
    assert outbox.sent == []


async def test_nothing_is_sent_without_an_owner(outbox):
    await delivery.send_run_result_email(
        _Session(None),
        owner_user_id=None,
        automation_name="Weekly digest",
        status="succeeded",
        result={"final_message": "x"},
    )
    assert outbox.sent == []


async def test_a_missing_owner_account_sends_nothing(outbox):
    await delivery.send_run_result_email(
        _Session(None),
        owner_user_id=uuid.uuid4(),
        automation_name="Weekly digest",
        status="succeeded",
        result={"final_message": "x"},
    )
    assert outbox.sent == []


async def test_a_send_failure_is_swallowed(outbox, monkeypatch):
    from app.mailer import MailerUnavailableError

    class _Boom:
        async def send(self, _message):
            raise MailerUnavailableError("smtp down")

    monkeypatch.setattr(delivery, "get_mailer", lambda: _Boom())
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    await delivery.send_run_result_email(
        _Session(user),
        owner_user_id=user.id,
        automation_name="Weekly digest",
        status="succeeded",
        result={"final_message": "x"},
    )  # does not raise


async def test_a_sent_email_is_logged_without_the_address(outbox, caplog):
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    with caplog.at_level("INFO", logger=delivery.logger.name):
        await delivery.send_run_result_email(
            _Session(user),
            owner_user_id=user.id,
            automation_id=3,
            automation_name="Weekly digest",
            status="succeeded",
            result={"final_message": "x"},
        )
    assert len(outbox.sent) == 1
    sent_lines = [r.getMessage() for r in caplog.records if "sent" in r.getMessage()]
    assert sent_lines, "a successful send leaves a log line"
    assert "Automation 3" in sent_lines[0]
    assert str(user.id) in sent_lines[0]
    assert "owner@example.com" not in caplog.text


async def test_mail_off_is_logged_not_silent(outbox, monkeypatch, caplog):
    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: False)
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    with caplog.at_level("WARNING", logger=delivery.logger.name):
        await delivery.send_run_result_email(
            _Session(user),
            owner_user_id=user.id,
            automation_id=3,
            automation_name="Weekly digest",
            status="succeeded",
            result={"final_message": "x"},
        )
    assert outbox.sent == []
    assert "SMTP is off" in caplog.text


async def test_a_mail_misconfiguration_is_swallowed_and_logged(
    outbox, monkeypatch, caplog
):
    """``SmtpMailer()`` raises ``ValueError`` on a missing host or From. The run
    is already committed as succeeded by then, so this must not escape."""

    def _misconfigured():
        raise ValueError("SMTP_HOST is required when SMTP_ENABLED is true")

    monkeypatch.setattr(delivery, "get_mailer", _misconfigured)
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    with caplog.at_level("ERROR", logger=delivery.logger.name):
        await delivery.send_run_result_email(
            _Session(user),
            owner_user_id=user.id,
            automation_id=3,
            automation_name="Weekly digest",
            status="succeeded",
            result={"final_message": "x"},
        )  # does not raise
    assert "SMTP_HOST is required" in caplog.text
