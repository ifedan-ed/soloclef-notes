"""A queued email survives the trip through Redis, and knows when it is too late."""

from __future__ import annotations

import pytest

from app.mail_queue.job import MailJob
from app.mailer.protocol import Attachment, OutboundEmail

pytestmark = pytest.mark.unit


def _job(**overrides) -> MailJob:
    message = OutboundEmail(
        to="a@example.test",
        subject="S",
        text_body="t",
        html_body="<p>h</p>",
        sender="n@example.test",
        reply_to="r@example.test",
        attachments=(Attachment(filename="f.bin", content=b"\x00\x01"),),
    )
    return MailJob(message=message, kind="invite", ref="7", **overrides)


def test_round_trips_every_field():
    job = _job(not_after=123.5)
    again = MailJob.from_json(job.to_json())
    assert again == job


def test_ids_are_unique():
    assert _job().id != _job().id


def test_expiry():
    assert not _job().expired(now=10**12), "no deadline never expires"
    job = _job(not_after=100.0)
    assert not job.expired(now=99.9)
    assert job.expired(now=100.0)


def test_next_attempt_keeps_the_id():
    job = _job()
    nxt = job.next_attempt()
    assert (nxt.id, nxt.attempt) == (job.id, 1)
