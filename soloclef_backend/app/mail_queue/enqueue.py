"""Hand an email to the queue; the request goes on without waiting for it."""

from __future__ import annotations

import logging
from datetime import datetime

import redis.asyncio as aioredis
from redis.exceptions import RedisError

from app.config import config
from app.mailer.protocol import OutboundEmail

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
    """Queue ``message`` and return its id (the id every log line carries).

    ``not_after``: when the link or code in it stops working; the senders
    drop it unsent after that.
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
        await QueueStore(client).push(job.to_json())
    except RedisError as exc:
        raise MailQueueUnavailableError(type(exc).__name__) from exc
    finally:
        await client.aclose()
    logger.info("Mail %s (%s, ref %s) queued", job.id, kind, job.ref)
    return job.id
