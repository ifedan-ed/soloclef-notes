"""At most N full video renders run at once across every worker process."""

from __future__ import annotations

import asyncio

import pytest
from redis.exceptions import ConnectionError as RedisConnectionError

from app.deliverables.video import render_slot

pytestmark = pytest.mark.unit


class _FakeLock:
    def __init__(self, store: dict, key: str) -> None:
        self.store, self.key = store, key

    async def acquire(self, blocking: bool = True) -> bool:
        if self.store.get(self.key):
            return False
        self.store[self.key] = True
        return True

    async def release(self) -> None:
        self.store.pop(self.key, None)

    async def extend(self, *_args, **_kwargs) -> bool:
        return True


class _FakeRedis:
    def __init__(self, store: dict) -> None:
        self.store = store

    def lock(self, key, timeout=None):
        return _FakeLock(self.store, key)

    async def aclose(self) -> None:
        return None


@pytest.fixture
def fake_redis(monkeypatch):
    store: dict = {}
    monkeypatch.setattr(render_slot.redis, "from_url", lambda *_a, **_k: _FakeRedis(store))
    monkeypatch.setattr(render_slot, "_POLL_SECONDS", 0.01)
    return store


async def test_a_second_render_waits_for_the_first(fake_redis):
    order: list[str] = []

    async def render(name: str, hold: float) -> None:
        async with render_slot.host_render_slot(slots=1, wait=5):
            order.append(f"{name} start")
            await asyncio.sleep(hold)
            order.append(f"{name} end")

    await asyncio.gather(render("a", 0.1), render("b", 0.0))
    assert order in (
        ["a start", "a end", "b start", "b end"],
        ["b start", "b end", "a start", "a end"],
    )
    assert not fake_redis, "the slot is released afterwards"


async def test_two_slots_let_two_renders_overlap(fake_redis):
    running = 0
    peak = 0

    async def render() -> None:
        nonlocal running, peak
        async with render_slot.host_render_slot(slots=2, wait=5):
            running += 1
            peak = max(peak, running)
            await asyncio.sleep(0.05)
            running -= 1

    await asyncio.gather(render(), render(), render())
    assert peak == 2


async def test_a_redis_outage_never_blocks_a_video(monkeypatch):
    class _Down:
        def lock(self, *_a, **_k):
            class _L:
                async def acquire(self, blocking=True):
                    raise RedisConnectionError("down")

            return _L()

        async def aclose(self):
            return None

    monkeypatch.setattr(render_slot.redis, "from_url", lambda *_a, **_k: _Down())
    ran = False
    async with render_slot.host_render_slot(slots=1, wait=1):
        ran = True
    assert ran


async def test_no_free_slot_in_time_still_renders(fake_redis):
    fake_redis[f"{render_slot.SLOT_KEY_PREFIX}:0"] = True
    ran = False
    async with render_slot.host_render_slot(slots=1, wait=0.05):
        ran = True
    assert ran
