"""Queueing the same mail twice: the second push is refused, a retry is not.

A retry never comes through here -- a failed job is written straight onto the
retry set by the sender -- which is why this guard cannot swallow one. The
tests that prove that live with the sender.
"""

from __future__ import annotations

import pytest
from redis.exceptions import ConnectionError as RedisConnectionError

from app.mail_queue import (
    MailQueueUnavailableError,
    enqueue as enqueue_module,
    enqueue_email,
)
from app.mail_queue.queue_store import READY
from app.mailer.protocol import OutboundEmail
from tests.unit.mail_queue.fake_redis import FakeRedis

pytestmark = pytest.mark.unit


class _Client(FakeRedis):
    """FakeRedis plus the one list command the push makes."""

    def __init__(self, push_fails: bool = False) -> None:
        super().__init__()
        self.pushed: list[str] = []
        self.push_fails = push_fails
        self.closed = 0

    async def lpush(self, key, value):
        assert key == READY
        if self.push_fails:
            raise RedisConnectionError("no route to the queue")
        self.pushed.append(value)
        return len(self.pushed)

    def register_script(self, _script):
        """QueueStore registers the retry script on construction; the push
        side never runs it."""

        async def never_called(**_kwargs):
            raise AssertionError("the push side does not promote retries")

        return never_called

    async def aclose(self):
        self.closed += 1


@pytest.fixture
def client(monkeypatch) -> _Client:
    fake = _Client()
    monkeypatch.setattr(enqueue_module.aioredis, "from_url", lambda *a, **k: fake)
    return fake


def _message(key: str | None) -> OutboundEmail:
    return OutboundEmail(
        to="person@example.test", subject="Reset", text_body="x", idempotency_key=key
    )


async def test_the_same_mail_queued_twice_is_queued_once(client):
    first = await enqueue_email(_message("password_reset:abc"), kind="password_reset")
    second = await enqueue_email(_message("password_reset:abc"), kind="password_reset")
    assert len(client.pushed) == 1
    assert second == first


async def test_a_second_mail_with_its_own_key_is_queued(client):
    await enqueue_email(_message("password_reset:abc"), kind="password_reset")
    await enqueue_email(_message("password_reset:def"), kind="password_reset")
    assert len(client.pushed) == 2


async def test_a_caller_with_no_key_is_queued_every_time(client):
    first = await enqueue_email(_message(None), kind="automation_result")
    second = await enqueue_email(_message(None), kind="automation_result")
    assert len(client.pushed) == 2
    assert first != second


async def test_a_push_that_never_landed_does_not_block_the_next_one(monkeypatch):
    fake = _Client(push_fails=True)
    monkeypatch.setattr(enqueue_module.aioredis, "from_url", lambda *a, **k: fake)
    with pytest.raises(MailQueueUnavailableError):
        await enqueue_email(_message("invite:7:1.0"), kind="invite")
    fake.push_fails = False
    assert await enqueue_email(_message("invite:7:1.0"), kind="invite")
    assert len(fake.pushed) == 1


async def test_a_refused_duplicate_is_logged_without_the_callers_key(client, caplog):
    caplog.set_level("INFO")
    await enqueue_email(
        _message("password_reset:tok-secret-123"), kind="password_reset"
    )
    await enqueue_email(
        _message("password_reset:tok-secret-123"), kind="password_reset"
    )
    assert "tok-secret-123" not in caplog.text
    assert "already queued" in caplog.text
