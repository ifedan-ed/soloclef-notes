"""The handful of Redis commands the dedupe keys are kept with, in memory.

Only ``SET`` (with ``NX``, ``GET`` and ``EX``), ``GET`` and ``DEL``: the two
dedupe stores use nothing else, and a fake that offers nothing else cannot
hide a command the real store does not make. Expiry runs off a clock the test
moves, so the window can be stepped over without waiting a day.
"""

from __future__ import annotations


class FakeRedis:
    def __init__(self) -> None:
        self.now = 0.0
        self._values: dict[str, str] = {}
        self._expires_at: dict[str, float] = {}

    def _evict(self, key: str) -> None:
        deadline = self._expires_at.get(key)
        if deadline is not None and self.now >= deadline:
            self._values.pop(key, None)
            self._expires_at.pop(key, None)

    async def set(self, key, value, *, nx=False, get=False, ex=None):
        self._evict(key)
        previous = self._values.get(key)
        if nx and previous is not None:
            return previous if get else False
        self._values[key] = value
        if ex is not None:
            self._expires_at[key] = self.now + ex
        return previous if get else True

    async def get(self, key):
        self._evict(key)
        return self._values.get(key)

    async def delete(self, key):
        self._evict(key)
        return 1 if self._values.pop(key, None) is not None else 0
