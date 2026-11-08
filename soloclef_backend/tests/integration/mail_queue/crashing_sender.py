"""A mail sender that really dies, for the two crash cases.

Not a mocked exception: this is a separate interpreter that stops at an
instruction with SIGKILL's finality, leaving Redis exactly as a power cut
would. The settings arrive as JSON on stdin.

    before_handover: take a message and die with it held, before the mail
        server has heard of it. The next run must send it.
    after_mark: take a message, send it, mark it handed over -- and die before
        the queue entry is dropped, which is the failure the queue's own notes
        admit. The next run must not send it again.
"""

from __future__ import annotations

import asyncio
import json
import os
import sys

import redis.asyncio as aioredis

from app.config import config
from app.mail_queue.dedupe.handover_guard import HandoverGuard
from app.mail_queue.queue_store import QueueStore
from app.mail_queue.sender import MailSender
from app.mailer import build_mailer

CASE = json.load(sys.stdin)


class _DyingStore:
    """The queue, except that finishing with a message kills the process."""

    def __init__(self, store: QueueStore) -> None:
        self._store = store

    async def take(self, sender, wait_seconds):
        return await self._store.take(sender, wait_seconds)

    async def done(self, sender, raw):
        sys.stdout.flush()
        os._exit(137)

    async def retry_later(self, sender, raw, new_raw, due):
        sys.stdout.flush()
        os._exit(137)


def _settings() -> None:
    config.SMTP_ENABLED = True
    config.MAIL_TRANSPORT = "smtp"
    config.SMTP_HOST = CASE["smtp_host"]
    config.SMTP_PORT = CASE["smtp_port"]
    config.SMTP_SECURITY = "none"
    config.SMTP_USERNAME = ""
    config.SMTP_PASSWORD = ""
    config.SMTP_TIMEOUT_SECONDS = 10
    config.SMTP_FROM = CASE["default_sender"]
    config.REDIS_APP_URL = CASE["redis_url"]


async def main() -> None:
    _settings()
    client = aioredis.from_url(CASE["redis_url"], decode_responses=True)
    store = QueueStore(client)
    guard = HandoverGuard(client)
    raw = await store.take(0, 10.0)
    if raw is None:
        raise SystemExit("nothing was queued for the crashing sender to take")
    print("took", flush=True)
    if CASE["case"] == "before_handover":
        sys.stdout.flush()
        os._exit(137)
    sender = MailSender(0, _DyingStore(store), build_mailer(), guard)
    await sender.handle(raw)
    raise SystemExit("the process was meant to die before this")


asyncio.run(main())
