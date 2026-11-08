"""The send guard: a message handed over once is not handed over twice.

This is the failure the queue could not fix at the push, because the message
was queued exactly once: a sender dies between giving the message to the mail
server and dropping it from its held list, and the next run puts it back.

The trap it must not fall into is the retry. A temporary failure re-queues the
same mail id at the next attempt, so a guard that keyed on the id alone would
turn every transient SMTP failure into a mail that never arrives and never
errors.
"""

from __future__ import annotations

import asyncio

import pytest

from app.mail_queue.dedupe import keys
from app.mail_queue.dedupe.handover_guard import HandoverGuard
from app.mail_queue.job import MailJob
from app.mail_queue.sender import MailSender
from app.mailer.protocol import MailerUnavailableError, OutboundEmail
from tests.unit.mail_queue.fake_redis import FakeRedis

pytestmark = pytest.mark.unit

NOW = 1_000_000.0


class _Store:
    def __init__(self) -> None:
        self.done_raw: list[str] = []
        self.retries: list[tuple[str, MailJob, float]] = []

    async def done(self, sender, raw):
        self.done_raw.append(raw)

    async def retry_later(self, sender, raw, new_raw, due):
        self.retries.append((raw, MailJob.from_json(new_raw), due))


class _Mailer:
    def __init__(self, error: Exception | None = None) -> None:
        self.error = error
        self.sent = []

    async def send(self, message):
        if self.error is not None:
            raise self.error
        self.sent.append(message)

    async def close(self):
        pass


def _job(**overrides) -> MailJob:
    message = OutboundEmail(to="person@example.test", subject="Reset", text_body="x")
    fields = {"message": message, "kind": "password_reset", "ref": "u1"}
    fields.update(overrides)
    return MailJob(**fields)


def _sender(mailer, guard, index: int = 0) -> tuple[MailSender, _Store]:
    store = _Store()
    return MailSender(index, store, mailer, guard, clock=lambda: NOW), store


async def test_a_sent_message_is_marked_as_handed_over():
    guard = HandoverGuard(FakeRedis())
    sender, _ = _sender(_Mailer(), guard)
    job = _job()
    assert await sender.handle(job.to_json()) == "sent"
    assert await guard.claim(job.id, job.attempt) == keys.SENT


async def test_the_same_attempt_put_back_by_a_restart_is_not_sent_again():
    guard = HandoverGuard(FakeRedis())
    mailer = _Mailer()
    sender, store = _sender(mailer, guard)
    raw = _job().to_json()
    assert await sender.handle(raw) == "sent"
    assert await sender.handle(raw) == "duplicate"
    assert len(mailer.sent) == 1
    assert store.done_raw == [raw, raw]


async def test_a_retry_of_a_failed_job_still_sends():
    guard = HandoverGuard(FakeRedis())
    failing, _ = _sender(_Mailer(MailerUnavailableError("451 later")), guard)
    job = _job()
    assert await failing.handle(job.to_json()) == "retry"

    working = _Mailer()
    retrying, _ = _sender(working, guard, index=1)
    assert await retrying.handle(job.next_attempt().to_json()) == "sent"
    assert len(working.sent) == 1


async def test_two_senders_racing_one_message_send_it_once():
    guard = HandoverGuard(FakeRedis())
    raw = _job().to_json()
    one, two = _Mailer(), _Mailer()
    first, _ = _sender(one, guard, index=0)
    second, _ = _sender(two, guard, index=1)
    outcomes = await asyncio.gather(first.handle(raw), second.handle(raw))
    assert sorted(outcomes) == ["duplicate", "sent"]
    assert len(one.sent) + len(two.sent) == 1


async def test_a_message_whose_claim_was_released_is_sent_again():
    guard = HandoverGuard(FakeRedis())
    job = _job()
    await guard.claim(job.id, job.attempt)
    await guard.release(job.id, job.attempt)
    mailer = _Mailer()
    sender, _ = _sender(mailer, guard)
    assert await sender.handle(job.to_json()) == "sent"
    assert len(mailer.sent) == 1


async def test_an_expired_message_never_takes_a_claim():
    guard = HandoverGuard(FakeRedis())
    sender, _ = _sender(_Mailer(), guard)
    job = _job(not_after=NOW - 1)
    assert await sender.handle(job.to_json()) == "expired"
    assert await guard.claim(job.id, job.attempt) is None


async def test_a_refused_duplicate_logs_the_id_and_never_the_address(caplog):
    caplog.set_level("INFO")
    guard = HandoverGuard(FakeRedis())
    sender, _ = _sender(_Mailer(), guard)
    job = _job()
    await sender.handle(job.to_json())
    caplog.clear()
    await sender.handle(job.to_json())
    assert job.id in caplog.text
    assert "person@example.test" not in caplog.text
