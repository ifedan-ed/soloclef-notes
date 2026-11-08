"""One sender's decisions: send, retry, drop, and what it logs."""

from __future__ import annotations

import pytest

from app.mail_queue.job import MailJob
from app.mail_queue.retry_policy import MAX_ATTEMPTS, RETRY_DELAYS_SECONDS
from app.mail_queue.sender import MailSender
from app.mailer.protocol import (
    MailerMisconfiguredError,
    MailerRejectedError,
    MailerUnavailableError,
    OutboundEmail,
)

pytestmark = pytest.mark.unit

SECRET = "tok-secret-123"
ADDRESS = "person@example.test"


class _Store:
    """The QueueStore calls a sender makes, in memory."""

    def __init__(self):
        self.done_raw = []
        self.retries = []

    async def done(self, sender, raw):
        self.done_raw.append(raw)

    async def retry_later(self, sender, raw, new_raw, due):
        self.retries.append((raw, MailJob.from_json(new_raw), due))


class _Mailer:
    def __init__(self, error: Exception | None = None):
        self.error = error
        self.sent = []

    async def send(self, message):
        if self.error is not None:
            raise self.error
        self.sent.append(message)

    async def close(self):
        pass


NOW = 1_000_000.0


def _raw(**overrides) -> str:
    message = OutboundEmail(to=ADDRESS, subject="Reset", text_body=f"link #{SECRET}")
    fields = {"message": message, "kind": "password_reset", "ref": "u1"}
    fields.update(overrides)
    return MailJob(**fields).to_json()


def _sender(mailer) -> tuple[MailSender, _Store]:
    store = _Store()
    return MailSender(0, store, mailer, clock=lambda: NOW), store


async def test_sends_and_marks_done():
    mailer = _Mailer()
    sender, store = _sender(mailer)
    raw = _raw()
    assert await sender.handle(raw) == "sent"
    assert len(mailer.sent) == 1
    assert store.done_raw == [raw]


async def test_an_expired_code_is_dropped_unsent():
    mailer = _Mailer()
    sender, store = _sender(mailer)
    assert await sender.handle(_raw(not_after=NOW - 1)) == "expired"
    assert mailer.sent == []
    assert len(store.done_raw) == 1


@pytest.mark.parametrize(
    "error", [MailerRejectedError("550"), MailerMisconfiguredError("535")]
)
async def test_permanent_failures_are_not_retried(error):
    sender, store = _sender(_Mailer(error))
    assert await sender.handle(_raw()) == "failed"
    assert store.retries == []
    assert len(store.done_raw) == 1


async def test_a_bug_is_not_retried():
    sender, store = _sender(_Mailer(RuntimeError("bug")))
    assert await sender.handle(_raw()) == "failed"
    assert store.retries == []


async def test_a_temporary_failure_is_retried_later():
    sender, store = _sender(_Mailer(MailerUnavailableError("451")))
    raw = _raw()
    assert await sender.handle(raw) == "retry"
    [(old, job, due)] = store.retries
    assert old == raw
    assert job.attempt == 1
    assert due == NOW + RETRY_DELAYS_SECONDS[0]
    assert store.done_raw == [], "moved to retry in the same step, not marked done"


async def test_gives_up_after_the_last_try():
    sender, store = _sender(_Mailer(MailerUnavailableError("451")))
    assert await sender.handle(_raw(attempt=MAX_ATTEMPTS - 1)) == "gave_up"
    assert store.retries == []
    assert len(store.done_raw) == 1


async def test_a_retry_after_the_code_expires_is_not_scheduled():
    sender, store = _sender(_Mailer(MailerUnavailableError("451")))
    soon = NOW + RETRY_DELAYS_SECONDS[0] - 1
    assert await sender.handle(_raw(not_after=soon)) == "expired"
    assert store.retries == []


async def test_an_unreadable_entry_is_dropped():
    sender, store = _sender(_Mailer())
    assert await sender.handle("{not json") == "unreadable"
    assert store.done_raw == ["{not json"]


@pytest.mark.parametrize(
    "error",
    [
        None,
        MailerUnavailableError("451"),
        MailerRejectedError("550"),
        RuntimeError("x"),
    ],
)
async def test_every_outcome_logs_the_id_but_never_the_code_or_address(error, caplog):
    caplog.set_level("DEBUG")
    sender, _ = _sender(_Mailer(error))
    raw = _raw()
    await sender.handle(raw)
    job_id = MailJob.from_json(raw).id
    assert job_id in caplog.text
    assert SECRET not in caplog.text
    assert ADDRESS not in caplog.text
