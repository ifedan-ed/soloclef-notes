"""One push per mail: the ledger of which mail holds a caller's key."""

from __future__ import annotations

import redis.asyncio as aioredis

from .keys import WINDOW_SECONDS, queued_key


class QueuedOnce:
    def __init__(self, client: aioredis.Redis) -> None:
        self._redis = client

    async def claim(self, idempotency_key: str, mail_id: str) -> str | None:
        """Take ``idempotency_key`` for ``mail_id``.

        Returns the id of the mail already holding it, or None when this mail
        took it. One round trip, and the taking and the reading are the same
        operation, so two requests racing cannot both be told they are first.
        """
        return await self._redis.set(
            queued_key(idempotency_key),
            mail_id,
            nx=True,
            get=True,
            ex=WINDOW_SECONDS,
        )

    async def release(self, idempotency_key: str, mail_id: str) -> None:
        """Give the key back after a push that never landed.

        Without this a mail Redis refused would hold its own identity for a
        day and the caller could never queue it again.
        """
        key = queued_key(idempotency_key)
        if await self._redis.get(key) == mail_id:
            await self._redis.delete(key)
