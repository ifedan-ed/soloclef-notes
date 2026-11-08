"""Put one event on the stream."""

from __future__ import annotations

from typing import Any

import redis.asyncio as aioredis

from app.config import config
from app.event_bus.event import Event

from .wire import RETENTION_ENTRIES, STREAM, encode


async def add(client: Any, event: Event) -> str:
    """Append ``event`` and return the entry id the stream gave it."""
    return await client.xadd(
        STREAM, encode(event), maxlen=RETENTION_ENTRIES, approximate=True
    )


async def add_with_own_client(event: Event) -> str:
    """Append ``event`` over a connection of this call's own."""
    # A client per call: Celery tasks each run on a fresh event loop, and an
    # asyncio Redis client belongs to the loop it first ran on.
    client = aioredis.from_url(config.REDIS_APP_URL, decode_responses=True)
    try:
        return await add(client, event)
    finally:
        await client.aclose()
