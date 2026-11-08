"""How often one person may start or poll a Nextcloud login.

Per user rather than per address, so a shared office IP does not lock out
colleagues; a fixed window in Redis, shared by every backend worker.
"""

from __future__ import annotations

from typing import Protocol
from uuid import UUID

from app.config import config

START_WINDOW = 10 * 60
POLL_LIMIT = (90, 60)  # the page polls every 2 s; room for two tabs


class Counter(Protocol):
    async def incr(self, key: str) -> int: ...
    async def expire(self, key: str, seconds: int, nx: bool = False) -> object: ...


class AttemptLimits:
    def __init__(self, counter: Counter) -> None:
        self._counter = counter

    async def _allow(self, kind: str, user_id: UUID, limit: tuple[int, int]) -> bool:
        most, window = limit
        key = f"soloclef:nextcloud:login-{kind}:{user_id}"
        count = await self._counter.incr(key)
        if count == 1:
            await self._counter.expire(key, window)
        return count <= most

    async def may_start(self, user_id: UUID) -> bool:
        limit = (config.NEXTCLOUD_LOGIN_START_LIMIT, START_WINDOW)
        return await self._allow("start", user_id, limit)

    async def may_poll(self, user_id: UUID) -> bool:
        return await self._allow("poll", user_id, POLL_LIMIT)
