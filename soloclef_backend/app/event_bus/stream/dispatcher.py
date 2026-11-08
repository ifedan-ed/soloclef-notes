"""Read the event stream as a group and hand each event to the local bus.

One consumer group over however many readers a deployment runs, so an event is
dispatched once — as the in-process bus dispatched it once, in whichever process
published it.

**The entry is acknowledged before it is dispatched.** That keeps the
at-most-once delivery the in-process bus had: a reader that dies mid-dispatch
loses the event, and no subscriber ever sees one twice. Acknowledging after
would turn every crash and every restart into a second automation run.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
from typing import Any

from redis.exceptions import RedisError, ResponseError

from app.event_bus.bus import EventBus, bus as default_bus

from .wire import GROUP, STREAM, decode

logger = logging.getLogger(__name__)

READ_BLOCK_MS = 5_000
READ_BATCH = 64
RETRY_SECONDS = 2.0


class StreamDispatcher:
    """One reader in the dispatch group."""

    def __init__(
        self,
        client: Any,
        *,
        bus: EventBus = default_bus,
        consumer: str,
        batch: int = READ_BATCH,
        block_ms: int | None = READ_BLOCK_MS,
    ) -> None:
        self._client = client
        self._bus = bus
        self._consumer = consumer
        self._batch = batch
        self._block_ms = block_ms

    async def ensure_group(self) -> None:
        """Create the group at the stream's tail, tolerating one that exists.

        At the tail, so a dispatcher starting for the first time inherits the
        history as read rather than replaying facts the old bus already acted on.
        """
        try:
            await self._client.xgroup_create(STREAM, GROUP, id="$", mkstream=True)
        except ResponseError as exc:
            if "BUSYGROUP" not in str(exc):
                raise

    async def drain_once(self) -> int:
        """Read one batch and dispatch it in stream order. Returns the count."""
        response = await self._client.xreadgroup(
            GROUP,
            self._consumer,
            {STREAM: ">"},
            count=self._batch,
            block=self._block_ms,
        )
        delivered = 0
        for _stream, entries in response or []:
            for entry_id, fields in entries:
                await self._client.xack(STREAM, GROUP, entry_id)
                await self._deliver(entry_id, fields)
                delivered += 1
        return delivered

    async def run(self, stop: asyncio.Event) -> None:
        """Dispatch until ``stop`` is set.

        A stream it cannot reach is waited out rather than raised: the process
        this runs in serves requests, and one that refused to start because
        Redis was down would be a new way to lose the whole API.
        """
        joined = False
        while not stop.is_set():
            try:
                if not joined:
                    await self.ensure_group()
                    joined = True
                await self.drain_once()
            except RedisError as exc:
                logger.error("event stream unreachable: %s", exc)
                joined = False
                with contextlib.suppress(TimeoutError):
                    await asyncio.wait_for(stop.wait(), RETRY_SECONDS)

    async def _deliver(self, entry_id: str, fields: dict[str, Any]) -> None:
        try:
            event = decode(fields)
        except (KeyError, ValueError) as exc:
            logger.error("event stream entry %s is not an event: %s", entry_id, exc)
            return
        await self._bus.dispatch(event)
