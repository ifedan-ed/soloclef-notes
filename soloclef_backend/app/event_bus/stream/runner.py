"""Keep one dispatcher alive for the length of a process.

The application starts this; the subscribers then run here rather than in the
process that published, which is the whole point of the stream.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import os
import socket
from dataclasses import dataclass

import redis.asyncio as aioredis

from app.config import config
from app.event_bus.bus import EventBus, bus as default_bus

from .dispatcher import READ_BLOCK_MS, StreamDispatcher

logger = logging.getLogger(__name__)

# A dispatcher asked to stop is inside a blocking read, so the grace is that
# read's own block plus a second to finish the batch it comes back with.
STOP_GRACE_SECONDS = READ_BLOCK_MS / 1000 + 1


def consumer_name() -> str:
    """Name this reader so the group can tell it from another replica's."""
    return f"{socket.gethostname()}:{os.getpid()}"


@dataclass(slots=True)
class Dispatching:
    """A running dispatcher, and the way to stop it."""

    task: asyncio.Task[None]
    stop: asyncio.Event
    client: aioredis.Redis

    async def aclose(self) -> None:
        self.stop.set()
        with contextlib.suppress(TimeoutError, asyncio.CancelledError):
            await asyncio.wait_for(self.task, STOP_GRACE_SECONDS)
        if not self.task.done():
            self.task.cancel()
        await self.client.aclose()


async def start_dispatching(*, bus: EventBus = default_bus) -> Dispatching:
    """Read the event stream for as long as this process runs."""
    client = aioredis.from_url(config.REDIS_APP_URL, decode_responses=True)
    consumer = consumer_name()
    dispatcher = StreamDispatcher(client, bus=bus, consumer=consumer)
    stop = asyncio.Event()
    logger.info("Event stream: dispatching as %s", consumer)
    return Dispatching(
        task=asyncio.create_task(dispatcher.run(stop)), stop=stop, client=client
    )
