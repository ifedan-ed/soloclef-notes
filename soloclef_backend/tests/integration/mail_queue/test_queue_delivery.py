"""The mail queue end to end: real Redis, real SMTP into a local Mailpit.

Opt in with MAIL_QUEUE_INTEGRATION=1 and a throwaway Redis and Mailpit (this
test empties both):

    docker run -d --rm --name mq-redis-test -p 127.0.0.1:26399:6379 redis:8-alpine
    docker run -d --rm --name mq-mailpit-test -p 127.0.0.1:21025:1025 \
        -p 127.0.0.1:28025:8025 axllent/mailpit:v1.21
    MAIL_QUEUE_INTEGRATION=1 MAIL_TEST_REDIS_URL=redis://127.0.0.1:26399/0 \
        MAILPIT_SMTP_PORT=21025 MAILPIT_API_URL=http://127.0.0.1:28025 \
        uv run pytest tests/integration/mail_queue
"""

from __future__ import annotations

import asyncio
import os
import time
from urllib.parse import urlparse

import httpx
import pytest
import pytest_asyncio
import redis.asyncio as aioredis

from app.config import config
from app.mail_queue import enqueue_email, retry_policy, service
from app.mail_queue.job import MailJob
from app.mail_queue.queue_store import READY, RETRY, QueueStore, sending_key
from app.mailer.protocol import MailerUnavailableError, OutboundEmail

pytestmark = pytest.mark.integration

if os.getenv("MAIL_QUEUE_INTEGRATION") != "1":
    pytest.skip(
        "Mail queue end to end; set MAIL_QUEUE_INTEGRATION=1 with Redis and Mailpit",
        allow_module_level=True,
    )

REDIS_URL = os.environ["MAIL_TEST_REDIS_URL"]
MAILPIT_API = os.environ["MAILPIT_API_URL"]
SENDERS = 4
BURST = 300

if urlparse(REDIS_URL).hostname not in {"127.0.0.1", "localhost"}:
    pytest.exit("MAIL_TEST_REDIS_URL must be a local throwaway Redis", returncode=2)


@pytest.fixture(autouse=True)
def mail_settings(monkeypatch):
    monkeypatch.setattr(config, "SMTP_ENABLED", True)
    monkeypatch.setattr(config, "MAIL_TRANSPORT", "smtp")
    monkeypatch.setattr(config, "SMTP_HOST", "127.0.0.1")
    monkeypatch.setattr(config, "SMTP_PORT", int(os.environ["MAILPIT_SMTP_PORT"]))
    monkeypatch.setattr(config, "SMTP_USERNAME", "")
    monkeypatch.setattr(config, "SMTP_PASSWORD", "")
    monkeypatch.setattr(config, "SMTP_SECURITY", "none")
    monkeypatch.setattr(config, "SMTP_TIMEOUT_SECONDS", 10)
    monkeypatch.setattr(config, "SMTP_FROM", "notes@queue.test")
    monkeypatch.setattr(config, "REDIS_APP_URL", REDIS_URL)


@pytest_asyncio.fixture
async def redis_client():
    client = aioredis.from_url(REDIS_URL, decode_responses=True)
    await client.flushdb()
    yield client
    await client.flushdb()
    await client.aclose()


@pytest.fixture
def mailpit():
    with httpx.Client(base_url=MAILPIT_API, timeout=10.0) as client:
        client.delete("/api/v1/messages").raise_for_status()
        yield client


@pytest.fixture
def built_mailers(monkeypatch):
    """Every mailer the service builds, to count the SMTP sessions it opened."""
    mailers = []
    real = service.build_mailer

    def recording():
        mailer = real()
        mailers.append(mailer)
        return mailer

    monkeypatch.setattr(service, "build_mailer", recording)
    return mailers


def _message(n: int) -> OutboundEmail:
    return OutboundEmail(
        to=f"person{n}@queue.test", subject=f"burst {n:04d}", text_body=f"body {n}"
    )


def _subjects(mailpit: httpx.Client) -> list[str]:
    data = mailpit.get("/api/v1/messages", params={"limit": 1000}).json()
    return [m["Subject"] for m in data["messages"]]


async def _until_delivered(mailpit, count: int, timeout: float) -> list[str]:
    deadline = time.monotonic() + timeout
    while True:
        subjects = await asyncio.to_thread(_subjects, mailpit)
        if len(subjects) >= count or time.monotonic() > deadline:
            return subjects
        await asyncio.sleep(0.2)


async def _run_senders(client, stop):
    return asyncio.create_task(service.run_senders(client, SENDERS, stop))


async def test_a_burst_of_300_is_queued_at_once_and_all_delivered(
    redis_client, mailpit, built_mailers
):
    started = time.monotonic()
    for n in range(BURST):
        await enqueue_email(_message(n), kind="burst", ref=n)
    queue_seconds = time.monotonic() - started
    # The request side only writes to Redis: 300 in well under a few seconds.
    assert queue_seconds < 5, f"queueing 300 took {queue_seconds:.1f}s"
    assert await redis_client.llen(READY) == BURST

    stop = asyncio.Event()
    sending_started = time.monotonic()
    task = await _run_senders(redis_client, stop)
    subjects = await _until_delivered(mailpit, BURST, timeout=120)
    send_seconds = time.monotonic() - sending_started
    stop.set()
    await task

    assert sorted(subjects) == sorted(f"burst {n:04d}" for n in range(BURST))
    assert await redis_client.llen(READY) == 0
    for i in range(SENDERS):
        assert await redis_client.llen(sending_key(i)) == 0

    sessions = [m._connection.sessions_opened for m in built_mailers]
    assert len(sessions) == SENDERS
    # One kept session per sender, renewed every 100 messages: at most 7 in
    # all for 300 messages, where one session per message would be 300.
    assert sum(sessions) <= SENDERS + BURST // 100, sessions
    print(
        f"\nqueued 300 in {queue_seconds:.2f}s, sent in {send_seconds:.2f}s, "
        f"SMTP sessions per sender {sessions}"
    )


async def test_mail_held_by_a_stopped_run_is_sent_on_restart(redis_client, mailpit):
    held = MailJob(message=_message(1), kind="held").to_json()
    await redis_client.lpush(sending_key(2), held)

    stop = asyncio.Event()
    task = await _run_senders(redis_client, stop)
    subjects = await _until_delivered(mailpit, 1, timeout=30)
    stop.set()
    await task

    assert subjects == ["burst 0001"]
    assert await redis_client.llen(sending_key(2)) == 0


async def test_an_expired_code_email_is_never_sent(redis_client, mailpit):
    await enqueue_email(_message(1), kind="code", not_after=time.time() - 1)
    await enqueue_email(_message(2), kind="code", not_after=time.time() + 600)

    stop = asyncio.Event()
    task = await _run_senders(redis_client, stop)
    await _until_delivered(mailpit, 1, timeout=30)
    await asyncio.sleep(1)  # time for the expired one, had it gone out
    stop.set()
    await task

    assert _subjects(mailpit) == ["burst 0002"]


async def test_a_temporary_failure_is_retried_and_delivered(
    redis_client, mailpit, monkeypatch
):
    monkeypatch.setattr(retry_policy, "RETRY_DELAYS_SECONDS", (1, 1, 1, 1, 1, 1))
    real = service.build_mailer
    failures = {"left": 2}

    class _FlakyOnce:
        def __init__(self):
            self._inner = real()

        async def send(self, message):
            if failures["left"] > 0:
                failures["left"] -= 1
                raise MailerUnavailableError("451 try later")
            await self._inner.send(message)

        async def close(self):
            await self._inner.close()

    monkeypatch.setattr(service, "build_mailer", _FlakyOnce)
    await enqueue_email(_message(9), kind="retry")

    stop = asyncio.Event()
    task = await _run_senders(redis_client, stop)
    subjects = await _until_delivered(mailpit, 1, timeout=30)
    stop.set()
    await task

    assert subjects == ["burst 0009"]
    assert failures["left"] == 0
    assert await redis_client.zcard(RETRY) == 0
    assert await QueueStore(redis_client).depth() == {"ready": 0, "retry": 0}
