"""Queueing an automation's run-result email: owner only, best-effort."""

from __future__ import annotations

import uuid
from types import SimpleNamespace

import pytest

from app.automations.notify import delivery

pytestmark = pytest.mark.unit


class _Outbox:
    """Stands in for the mail queue."""

    def __init__(self):
        self.sent = []
        self.kinds = []

    async def enqueue(self, message, *, kind, ref=None, not_after=None):
        self.sent.append(message)
        self.kinds.append(kind)
        return f"mail-{len(self.sent)}"


@pytest.fixture
def outbox(monkeypatch):
    box = _Outbox()
    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: True)
    monkeypatch.setattr(delivery, "enqueue_email", box.enqueue)
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
        run_id=11,
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
        run_id=11,
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
        run_id=11,
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
        run_id=11,
        owner_user_id=user.id,
        automation_name="Weekly digest",
        status="succeeded",
        result={"final_message": "x"},
    )
    assert outbox.sent == []


async def test_nothing_is_sent_without_an_owner(outbox):
    await delivery.send_run_result_email(
        _Session(None),
        run_id=11,
        owner_user_id=None,
        automation_name="Weekly digest",
        status="succeeded",
        result={"final_message": "x"},
    )
    assert outbox.sent == []


async def test_a_missing_owner_account_sends_nothing(outbox):
    await delivery.send_run_result_email(
        _Session(None),
        run_id=11,
        owner_user_id=uuid.uuid4(),
        automation_name="Weekly digest",
        status="succeeded",
        result={"final_message": "x"},
    )
    assert outbox.sent == []


async def test_a_queue_failure_is_swallowed(outbox, monkeypatch):
    from app.mail_queue import MailQueueUnavailableError

    async def _boom(_message, **_):
        raise MailQueueUnavailableError("ConnectionError")

    monkeypatch.setattr(delivery, "enqueue_email", _boom)
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    await delivery.send_run_result_email(
        _Session(user),
        run_id=11,
        owner_user_id=user.id,
        automation_name="Weekly digest",
        status="succeeded",
        result={"final_message": "x"},
    )  # does not raise


async def test_a_queued_email_is_logged_without_the_address(outbox, caplog):
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    with caplog.at_level("INFO", logger=delivery.logger.name):
        await delivery.send_run_result_email(
            _Session(user),
            run_id=11,
            owner_user_id=user.id,
            automation_id=3,
            automation_name="Weekly digest",
            status="succeeded",
            result={"final_message": "x"},
        )
    assert len(outbox.sent) == 1
    assert outbox.kinds == ["automation_result"]
    lines = [r.getMessage() for r in caplog.records if "queued" in r.getMessage()]
    assert lines, "a queued email leaves a log line"
    assert "Automation 3" in lines[0]
    assert str(user.id) in lines[0]
    assert "mail-1" in lines[0], "the line names the mail id the sender logs"
    assert "owner@example.com" not in caplog.text


async def test_mail_off_is_logged_not_silent(outbox, monkeypatch, caplog):
    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: False)
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    with caplog.at_level("WARNING", logger=delivery.logger.name):
        await delivery.send_run_result_email(
            _Session(user),
            run_id=11,
            owner_user_id=user.id,
            automation_id=3,
            automation_name="Weekly digest",
            status="succeeded",
            result={"final_message": "x"},
        )
    assert outbox.sent == []
    assert "SMTP is off" in caplog.text


async def test_a_message_that_cannot_be_built_is_swallowed_and_logged(
    outbox, monkeypatch, caplog
):
    """The run is already committed as succeeded by then, so nothing escapes."""

    def _broken(**_):
        raise ValueError("bad template")

    monkeypatch.setattr(delivery, "build_success_email", _broken)
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    with caplog.at_level("ERROR", logger=delivery.logger.name):
        await delivery.send_run_result_email(
            _Session(user),
            run_id=11,
            owner_user_id=user.id,
            automation_id=3,
            automation_name="Weekly digest",
            status="succeeded",
            result={"final_message": "x"},
        )  # does not raise
    assert "could not be built: ValueError" in caplog.text
    assert outbox.sent == []


async def test_one_run_result_is_one_mail(outbox):
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    for _ in range(2):
        await delivery.send_run_result_email(
            _Session(user),
            run_id=42,
            owner_user_id=user.id,
            automation_id=3,
            automation_name="Weekly digest",
            status="succeeded",
            result={"final_message": "x"},
        )
    assert outbox.sent[0].idempotency_key == outbox.sent[1].idempotency_key
    assert "42" in outbox.sent[0].idempotency_key


async def test_another_run_of_the_same_automation_is_its_own_mail(outbox):
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    for run_id in (42, 43):
        await delivery.send_run_result_email(
            _Session(user),
            run_id=run_id,
            owner_user_id=user.id,
            automation_id=3,
            automation_name="Weekly digest",
            status="succeeded",
            result={"final_message": "x"},
        )
    assert outbox.sent[0].idempotency_key != outbox.sent[1].idempotency_key


async def test_a_run_that_ends_a_second_way_is_not_swallowed(outbox):
    """The timed-out sweep and the executor race for the same run.

    Only one of them writes the terminal status, so only one should mail; but
    if both ever did, the two mails say different things, and a key that
    swallowed the second would hide that rather than fix it.
    """
    user = SimpleNamespace(id=uuid.uuid4(), email="owner@example.com")
    for status in ("succeeded", "timed_out"):
        await delivery.send_run_result_email(
            _Session(user),
            run_id=42,
            owner_user_id=user.id,
            automation_id=3,
            automation_name="Weekly digest",
            status=status,
            result={"final_message": "x"},
            error={"message": "ran out of time"},
        )
    assert outbox.sent[0].idempotency_key != outbox.sent[1].idempotency_key
