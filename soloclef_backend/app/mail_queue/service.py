"""The mail_sender process: ``MAIL_SENDERS`` senders and the retry clock.

Run exactly one of these per deployment (compose service ``mail_sender``):
at start it puts back whatever the previous run's senders held, which is
only safe when no other copy is sending.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import signal
import time

import redis.asyncio as aioredis

from app.config import config
from app.mailer import build_mailer, is_mail_enabled

from .queue_store import QueueStore
from .sender import MailSender

logger = logging.getLogger(__name__)

RETRY_CLOCK_SECONDS = 1.0


def sender_count() -> int:
    raw = config.MAIL_SENDERS
    try:
        count = int(raw)
    except ValueError:
        count = 0
    if count < 1:
        raise ValueError(
            f"MAIL_SENDERS must be a whole number of at least 1 (got {raw!r})"
        )
    return count


async def _retry_clock(store: QueueStore, stop: asyncio.Event) -> None:
    while not stop.is_set():
        moved = await store.promote_due(time.time())
        if moved:
            logger.info("Mail queue: %s retries due, back in line", moved)
        with contextlib.suppress(TimeoutError):
            await asyncio.wait_for(stop.wait(), RETRY_CLOCK_SECONDS)


async def run_senders(client: aioredis.Redis, count: int, stop: asyncio.Event) -> None:
    """Run ``count`` senders until ``stop`` is set; each finishes its message."""
    store = QueueStore(client)
    held = await store.requeue_held()
    if held:
        logger.warning(
            "Mail queue: %s messages from a stopped run put back in line", held
        )
    # Every mailer is built before any sender starts, so bad settings stop
    # the service at once instead of failing each message.
    senders = [MailSender(i, store, build_mailer()) for i in range(count)]
    logger.info(
        "Mail sender: %s senders over %s; queue %s",
        count,
        config.MAIL_TRANSPORT,
        await store.depth(),
    )
    await asyncio.gather(_retry_clock(store, stop), *(s.run(stop) for s in senders))


async def main() -> None:
    if not is_mail_enabled():
        raise SystemExit("SMTP_ENABLED is off; the mail sender has nothing to do")
    count = sender_count()
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, stop.set)
    client = aioredis.from_url(config.REDIS_APP_URL, decode_responses=True)
    try:
        await run_senders(client, count, stop)
    finally:
        await client.aclose()
    logger.info("Mail sender stopped")
