"""A host-wide cap on how many full video renders run at once.

A full render uses its whole sandbox (SANDBOX_CPU cores, 6 by default). The
in-process gate in the deliverables sandbox tools only counts renders inside
one worker process, and the Celery worker runs several, so two videos started
together could each take six cores on a shared host. This takes one of
``VIDEO_SANDBOX_MAX_CONCURRENT_RENDERS`` Redis-held slots for the render's
duration, renewing it while the render runs; another render waits for a slot.

A slot is never a reason to fail a video: if Redis is unreachable, or a slot
is not free within the wait, the render proceeds and the miss is logged.
"""

from __future__ import annotations

import asyncio
import logging
from contextlib import asynccontextmanager, suppress

import redis.asyncio as redis
from redis.exceptions import LockError, RedisError

from app.config import config

logger = logging.getLogger(__name__)

SLOT_KEY_PREFIX = "video_render:host_slot"
# A slot expires on its own if its holder dies; it is renewed well before that.
SLOT_TTL_SECONDS = 600.0
# How long a render waits for a slot before rendering anyway.
SLOT_WAIT_SECONDS = 3600.0
_POLL_SECONDS = 5.0


@asynccontextmanager
async def host_render_slot(*, slots: int | None = None, wait: float = SLOT_WAIT_SECONDS):
    count = max(1, slots if slots is not None else config.VIDEO_SANDBOX_MAX_CONCURRENT_RENDERS)
    client = redis.from_url(config.REDIS_APP_URL, decode_responses=True)
    held = None
    keeper: asyncio.Task | None = None
    try:
        try:
            held = await _acquire(client, count, wait)
        except RedisError:
            logger.warning("Video render slot unavailable (Redis); rendering without it", exc_info=True)
        if held is None:
            logger.warning("No free video render slot after %.0fs; rendering anyway", wait)
        else:
            keeper = asyncio.create_task(_renew(held))
        yield
    finally:
        if keeper is not None:
            keeper.cancel()
            with suppress(asyncio.CancelledError):
                await keeper
        if held is not None:
            with suppress(LockError, RedisError):
                await held.release()
        with suppress(Exception):
            await client.aclose()


async def _acquire(client, count: int, wait: float):
    """The first free slot, trying each without blocking until ``wait`` ends."""
    deadline = asyncio.get_running_loop().time() + wait
    while True:
        for index in range(count):
            lock = client.lock(f"{SLOT_KEY_PREFIX}:{index}", timeout=SLOT_TTL_SECONDS)
            if await lock.acquire(blocking=False):
                return lock
        if asyncio.get_running_loop().time() >= deadline:
            return None
        await asyncio.sleep(_POLL_SECONDS)


async def _renew(lock) -> None:
    while True:
        await asyncio.sleep(SLOT_TTL_SECONDS / 3)
        try:
            await lock.extend(SLOT_TTL_SECONDS, replace_ttl=True)
        except (LockError, RedisError):
            logger.warning("Could not renew the video render slot", exc_info=True)
