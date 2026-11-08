"""Hand an email to the queue; the request goes on without waiting for it.

A caller that says what identifies its mail (``OutboundEmail.idempotency_key``)
gets one push per identity: the second is refused and told which mail already
holds it. A caller that says nothing is queued every time, as it always was.

A retry is not a second push. A failed job is written straight onto the retry
set by the sender, so it never comes back through here.
"""

from __future__ import annotations

import logging
from datetime import datetime

import redis.asyncio as aioredis
from redis.exceptions import RedisError

from app.config import config
from app.mailer.protocol import OutboundEmail

from .dedupe.queued_once import QueuedOnce
from .job import MailJob
from .queue_store import QueueStore

logger = logging.getLogger(__name__)


class MailQueueUnavailableError(RuntimeError):
    """Redis did not take the message; nothing will be sent."""


async def enqueue_email(
    message: OutboundEmail,
    *,
    kind: str,
    ref: object | None = None,
    not_after: datetime | float | None = None,
) -> str:
    """Queue ``message`` and return the id of the mail that will be sent.

    ``not_after``: when the link or code in it stops working; the senders
    drop it unsent after that.

    The id is this mail's own, unless ``message.idempotency_key`` is already
    held by one queued within the window -- then nothing is pushed and the id
    of that mail comes back, because the email is on its way either way.
    """
    if isinstance(not_after, datetime):
        not_after = not_after.timestamp()
    job = MailJob(
        message=message,
        kind=kind,
        ref=None if ref is None else str(ref),
        not_after=not_after,
    )
    # A client per call: Celery tasks run each on a fresh event loop, and an
    # asyncio Redis client belongs to the loop it first ran on.
    client = aioredis.from_url(config.REDIS_APP_URL, decode_responses=True)
    try:
        return await _push_once(client, job)
    except RedisError as exc:
        raise MailQueueUnavailableError(type(exc).__name__) from exc
    finally:
        await client.aclose()


async def _push_once(client: aioredis.Redis, job: MailJob) -> str:
    key = job.message.idempotency_key
    if not key:
        await QueueStore(client).push(job.to_json())
        logger.info("Mail %s (%s, ref %s) queued", job.id, job.kind, job.ref)
        return job.id

    ledger = QueuedOnce(client)
    held_by = await ledger.claim(key, job.id)
    if held_by is not None:
        # The caller's key is never logged: for a password reset it is built
        # out of the reset token.
        logger.info(
            "Mail (%s, ref %s) not queued: the same mail is already queued as %s",
            job.kind,
            job.ref,
            held_by,
        )
        return held_by
    try:
        await QueueStore(client).push(job.to_json())
    except RedisError:
        # Nothing was queued, so nothing may keep this identity: otherwise the
        # caller could not queue the same mail again for a whole day.
        await ledger.release(key, job.id)
        raise
    logger.info("Mail %s (%s, ref %s) queued", job.id, job.kind, job.ref)
    return job.id
