"""Putting back what a stopped run held, and whose claim is dropped with it.

A claim held by a sender that is gone cannot be told from one held by a sender
still working, so at the start of a run every claim belonging to recovered mail
is dropped and the message is offered again. A message already marked sent
keeps its mark, and is the one thing recovery does not undo.
"""

from __future__ import annotations

import pytest

from app.mail_queue import recovery
from app.mail_queue.dedupe import keys
from app.mail_queue.dedupe.handover_guard import HandoverGuard
from app.mail_queue.job import MailJob
from app.mailer.protocol import OutboundEmail
from tests.unit.mail_queue.fake_redis import FakeRedis

pytestmark = pytest.mark.unit


class _Queue:
    """The three moves recovery makes, over in-memory held lists."""

    def __init__(self, held: dict[str, list[str]]) -> None:
        self.held = held
        self.ready: list[str] = []

    async def held_keys(self):
        return list(self.held)

    async def newest_held(self, key):
        entries = self.held[key]
        return entries[-1] if entries else None

    async def put_back(self, key):
        entries = self.held[key]
        if not entries:
            return None
        raw = entries.pop()
        self.ready.append(raw)
        return raw


def _raw(**overrides) -> str:
    message = OutboundEmail(to="person@example.test", subject="Held", text_body="x")
    fields = {"message": message, "kind": "invite", "ref": "7"}
    fields.update(overrides)
    return MailJob(**fields).to_json()


async def test_every_held_message_goes_back_in_line():
    queue = _Queue({"soloclef:mail:sending:0": [_raw(), _raw()]})
    guard = HandoverGuard(FakeRedis())
    assert await recovery.requeue_held(queue, guard) == 2
    assert len(queue.ready) == 2


async def test_a_claim_left_by_a_stopped_sender_is_dropped():
    raw = _raw()
    job = MailJob.from_json(raw)
    guard = HandoverGuard(FakeRedis())
    await guard.claim(job.id, job.attempt)
    queue = _Queue({"soloclef:mail:sending:0": [raw]})

    await recovery.requeue_held(queue, guard)

    assert await guard.claim(job.id, job.attempt) is None


async def test_a_message_already_sent_keeps_that_mark():
    raw = _raw()
    job = MailJob.from_json(raw)
    guard = HandoverGuard(FakeRedis())
    await guard.claim(job.id, job.attempt)
    await guard.mark_sent(job.id, job.attempt)
    queue = _Queue({"soloclef:mail:sending:0": [raw]})

    await recovery.requeue_held(queue, guard)

    assert queue.ready == [raw]
    assert await guard.claim(job.id, job.attempt) == keys.SENT


async def test_an_unreadable_held_entry_is_still_put_back():
    queue = _Queue({"soloclef:mail:sending:0": ["not json"]})
    assert await recovery.requeue_held(queue, HandoverGuard(FakeRedis())) == 1
    assert queue.ready == ["not json"]
