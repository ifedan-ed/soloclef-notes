"""Real dedupe end to end: real Redis, real SMTP into Mailpit, real deaths.

The two failures are different and are proved differently. The same mail
queued twice is refused at the push. A message the mail server already took,
whose sender died before it could say so, is refused at the send -- and that
case is proved with an interpreter that really stops, not a mocked exception,
because what is being tested is what Redis is left holding.

Opt in exactly as ``test_queue_delivery.py`` does; its docstring has the
containers.
"""

from __future__ import annotations

import asyncio
import json
import os
import subprocess
import sys
import time
from urllib.parse import urlparse

import httpx
import pytest
import pytest_asyncio
import redis.asyncio as aioredis

from app.config import config
from app.mail_queue import enqueue_email, service
from app.mail_queue.dedupe import keys
from app.mail_queue.dedupe.handover_guard import HandoverGuard
from app.mail_queue.job import MailJob
from app.mail_queue.queue_store import READY, QueueStore, sending_key
from app.mail_queue.sender import MailSender
from app.mailer import build_mailer
from app.mailer.protocol import OutboundEmail

pytestmark = pytest.mark.integration

if os.getenv("MAIL_QUEUE_INTEGRATION") != "1":
    pytest.skip(
        "Mail queue end to end; set MAIL_QUEUE_INTEGRATION=1 with Redis and Mailpit",
        allow_module_level=True,
    )

REDIS_URL = os.environ["MAIL_TEST_REDIS_URL"]
MAILPIT_API = os.environ["MAILPIT_API_URL"]
SMTP_PORT = int(os.environ["MAILPIT_SMTP_PORT"])
DEFAULT_SENDER = "notes@queue.test"
HERE = os.path.dirname(os.path.abspath(__file__))

if urlparse(REDIS_URL).hostname not in {"127.0.0.1", "localhost"}:
    pytest.exit("MAIL_TEST_REDIS_URL must be a local throwaway Redis", returncode=2)


@pytest.fixture(autouse=True)
def mail_settings(monkeypatch):
    monkeypatch.setattr(config, "SMTP_ENABLED", True)
    monkeypatch.setattr(config, "MAIL_TRANSPORT", "smtp")
    monkeypatch.setattr(config, "SMTP_HOST", "127.0.0.1")
    monkeypatch.setattr(config, "SMTP_PORT", SMTP_PORT)
    monkeypatch.setattr(config, "SMTP_USERNAME", "")
    monkeypatch.setattr(config, "SMTP_PASSWORD", "")
    monkeypatch.setattr(config, "SMTP_SECURITY", "none")
    monkeypatch.setattr(config, "SMTP_TIMEOUT_SECONDS", 10)
    monkeypatch.setattr(config, "SMTP_FROM", DEFAULT_SENDER)
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


def _message(subject: str, key: str | None) -> OutboundEmail:
    return OutboundEmail(
        to="person@queue.test",
        subject=subject,
        text_body=f"{subject}\n",
        idempotency_key=key,
    )


def _subjects(mailpit: httpx.Client) -> list[str]:
    answer = mailpit.get("/api/v1/messages", params={"limit": 200}).json()
    return sorted(m["Subject"] for m in answer["messages"])


async def _until_delivered(mailpit, count: int, timeout: float) -> list[str]:
    deadline = time.monotonic() + timeout
    while True:
        subjects = _subjects(mailpit)
        if len(subjects) >= count or time.monotonic() > deadline:
            return subjects
        await asyncio.sleep(0.1)


async def _drain(
    client, mailpit, *, expect: int, senders: int = 2, timeout: float = 15.0
):
    """Run the real service until ``expect`` mails have arrived, then stop it."""
    stop = asyncio.Event()
    task = asyncio.create_task(service.run_senders(client, senders, stop))
    try:
        return await _until_delivered(mailpit, expect, timeout)
    finally:
        stop.set()
        await task


def _crash(case: str) -> subprocess.CompletedProcess:
    """Run a sender in its own interpreter that dies at ``case``."""
    payload = json.dumps(
        {
            "case": case,
            "redis_url": REDIS_URL,
            "smtp_host": "127.0.0.1",
            "smtp_port": SMTP_PORT,
            "default_sender": DEFAULT_SENDER,
        }
    )
    done = subprocess.run(
        [sys.executable, os.path.join(HERE, "crashing_sender.py")],
        input=payload,
        capture_output=True,
        text=True,
        timeout=120,
    )
    assert "took" in done.stdout, f"the sender never took a message: {done.stderr}"
    assert done.returncode == 137, (
        f"it did not die as told: {done.returncode} {done.stderr}"
    )
    return done


# --- the same mail queued twice -------------------------------------------


async def test_the_same_mail_queued_twice_is_delivered_once(redis_client, mailpit):
    first = await enqueue_email(_message("one only", "invite:7:1.0"), kind="invite")
    second = await enqueue_email(_message("one only", "invite:7:1.0"), kind="invite")
    assert second == first
    assert await redis_client.llen(READY) == 1

    subjects = await _drain(redis_client, mailpit, expect=1)
    await asyncio.sleep(1.0)  # time for a second, had one been queued
    assert _subjects(mailpit) == ["one only"]
    assert subjects == ["one only"]


async def test_once_the_window_passes_the_same_mail_is_sent_again(
    redis_client, mailpit
):
    key = "invite:7:1.0"
    await enqueue_email(_message("first time", key), kind="invite")
    # The window passing, exactly as Redis leaves it: the key is gone.
    assert await redis_client.pexpire(keys.queued_key(key), 1)
    await asyncio.sleep(0.1)

    await enqueue_email(_message("second time", key), kind="invite")
    assert await redis_client.llen(READY) == 2

    assert await _drain(redis_client, mailpit, expect=2) == [
        "first time",
        "second time",
    ]


async def test_a_caller_with_no_key_is_queued_and_sent_every_time(
    redis_client, mailpit
):
    await enqueue_email(_message("no key", None), kind="automation_result")
    await enqueue_email(_message("no key", None), kind="automation_result")
    assert await redis_client.llen(READY) == 2
    assert await _drain(redis_client, mailpit, expect=2) == ["no key", "no key"]


# --- a crash around the handover ------------------------------------------


async def test_a_crash_before_the_handover_sends_the_mail_on_restart(
    redis_client, mailpit
):
    await enqueue_email(_message("not yet sent", None), kind="invite")

    _crash("before_handover")

    assert _subjects(mailpit) == []
    assert await redis_client.llen(sending_key(0)) == 1

    assert await _drain(redis_client, mailpit, expect=1) == ["not yet sent"]


async def test_a_crash_after_the_handover_does_not_send_the_mail_again(
    redis_client, mailpit
):
    await enqueue_email(_message("already sent", None), kind="invite")

    _crash("after_mark")

    # The mail server took it and the sender died still holding the entry:
    # the failure the queue's own notes admit.
    assert _subjects(mailpit) == ["already sent"]
    assert await redis_client.llen(sending_key(0)) == 1
    raw = await redis_client.lindex(sending_key(0), -1)
    job = MailJob.from_json(raw)
    assert await redis_client.get(keys.handover_key(job.id, job.attempt)) == keys.SENT

    subjects = await _drain(redis_client, mailpit, expect=2, timeout=6.0)

    assert subjects == ["already sent"], "the restart sent it a second time"
    assert await redis_client.llen(sending_key(0)) == 0
    assert await redis_client.llen(READY) == 0


# --- two senders over one message -----------------------------------------


async def test_two_senders_handed_one_message_send_it_once(redis_client, mailpit):
    raw = MailJob(message=_message("raced", None), kind="invite", ref="7").to_json()
    store = QueueStore(redis_client)
    guard = HandoverGuard(redis_client)
    mailers = [build_mailer() for _ in range(2)]
    senders = [MailSender(i, store, mailers[i], guard) for i in range(2)]
    try:
        outcomes = await asyncio.gather(*(s.handle(raw) for s in senders))
    finally:
        for post in mailers:
            await post.close()

    assert sorted(outcomes) == ["duplicate", "sent"]
    assert await _until_delivered(mailpit, 1, 5.0) == ["raced"]
    await asyncio.sleep(1.0)
    assert _subjects(mailpit) == ["raced"]
