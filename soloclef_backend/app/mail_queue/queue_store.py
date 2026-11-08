"""The queue's Redis keys and the moves between them.

- ``ready``: a list; new mail is pushed on the left, senders take from the right.
- ``sending:<n>``: what sender ``n`` holds right now (moved there atomically
  as it is taken), so a sender that dies mid-send loses nothing: at start the
  service puts every held message back on ``ready`` (``recovery.py``), and
  the send guard is what stops one already sent going out again.
- ``retry``: a sorted set of failed mail by the time it is due again.
"""

from __future__ import annotations

import redis.asyncio as aioredis

PREFIX = "soloclef:mail"
READY = f"{PREFIX}:ready"
RETRY = f"{PREFIX}:retry"
SENDING_PATTERN = f"{PREFIX}:sending:*"


def sending_key(sender: int) -> str:
    return f"{PREFIX}:sending:{sender}"


# Move every retry due by ARGV[1] onto ready, a batch at a time.
_PROMOTE_DUE = """
local due = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, 500)
for _, raw in ipairs(due) do
  redis.call('ZREM', KEYS[1], raw)
  redis.call('LPUSH', KEYS[2], raw)
end
return #due
"""


class QueueStore:
    def __init__(self, client: aioredis.Redis) -> None:
        self._redis = client
        self._promote = client.register_script(_PROMOTE_DUE)

    async def push(self, raw: str) -> None:
        await self._redis.lpush(READY, raw)

    async def take(self, sender: int, wait_seconds: float) -> str | None:
        return await self._redis.blmove(
            READY, sending_key(sender), wait_seconds, "RIGHT", "LEFT"
        )

    async def done(self, sender: int, raw: str) -> None:
        await self._redis.lrem(sending_key(sender), 1, raw)

    async def retry_later(
        self, sender: int, raw: str, new_raw: str, due: float
    ) -> None:
        pipe = self._redis.pipeline(transaction=True)
        pipe.zadd(RETRY, {new_raw: due})
        pipe.lrem(sending_key(sender), 1, raw)
        await pipe.execute()

    async def promote_due(self, now: float) -> int:
        return int(await self._promote(keys=[RETRY, READY], args=[now]))

    async def held_keys(self) -> list[str]:
        """Every sender's held list, this run's and a stopped run's alike."""
        return [key async for key in self._redis.scan_iter(match=SENDING_PATTERN)]

    async def newest_held(self, key: str) -> str | None:
        """The entry ``put_back`` would move next, without moving it.

        Recovery reads a held message before it moves it, because what it has
        to do about that message depends on what the message is
        (``recovery.py``).
        """
        return await self._redis.lindex(key, -1)

    async def put_back(self, key: str) -> str | None:
        """Move one held message to the end of the line; None when none is left."""
        return await self._redis.lmove(key, READY, "RIGHT", "RIGHT")

    async def depth(self) -> dict[str, int]:
        return {
            "ready": int(await self._redis.llen(READY)),
            "retry": int(await self._redis.zcard(RETRY)),
        }
