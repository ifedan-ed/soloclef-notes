"""Hand-off codes and page tickets: spent once, gone after their lifetime, stored hashed."""

from __future__ import annotations

import pytest

from app.app_handoff.codes import (
    CODE_LIFETIME_SECONDS,
    TICKET_LIFETIME_SECONDS,
    HandoffGrant,
    MemoryStore,
    RedisStore,
)

pytestmark = pytest.mark.unit


class Clock:
    def __init__(self) -> None:
        self.now = 1_000.0

    def __call__(self) -> float:
        return self.now


def grant(clock: Clock, **overrides) -> HandoffGrant:
    values = {
        "purpose": "drive_picker",
        "user_id": "u-1",
        "workspace_id": 3,
        "notebook_id": 7,
        "issued_at": clock.now,
    }
    values.update(overrides)
    return HandoffGrant(**values)


def test_the_lifetimes_are_a_minute_and_ten_minutes():
    assert CODE_LIFETIME_SECONDS == 60
    assert TICKET_LIFETIME_SECONDS == 600


async def test_a_code_is_spent_once():
    clock = Clock()
    store = MemoryStore(CODE_LIFETIME_SECONDS, clock)
    code = await store.issue(grant(clock))
    assert len(code) >= 40
    assert await store.redeem(code) == grant(clock)
    assert await store.redeem(code) is None


async def test_a_code_past_its_lifetime_opens_nothing():
    clock = Clock()
    store = MemoryStore(CODE_LIFETIME_SECONDS, clock)
    code = await store.issue(grant(clock))
    clock.now += CODE_LIFETIME_SECONDS
    assert await store.redeem(code) is None


async def test_an_unknown_or_empty_code_opens_nothing():
    clock = Clock()
    store = MemoryStore(CODE_LIFETIME_SECONDS, clock)
    await store.issue(grant(clock))
    assert await store.redeem("not-a-code") is None
    assert await store.redeem("") is None


async def test_codes_are_kept_hashed_never_as_issued():
    clock = Clock()
    store = MemoryStore(CODE_LIFETIME_SECONDS, clock)
    code = await store.issue(grant(clock))
    assert code not in store._items
    assert all(len(key) == 64 for key in store._items)


class FakeRedis:
    def __init__(self) -> None:
        self.values: dict[str, str] = {}
        self.ttl: dict[str, int] = {}

    async def set(self, key, value, ex=None, nx=False):
        if nx and key in self.values:
            return None
        self.values[key] = value
        self.ttl[key] = ex
        return True

    async def getdel(self, key):
        self.ttl.pop(key, None)
        return self.values.pop(key, None)


class DownRedis:
    async def set(self, *args, **kwargs):
        raise ConnectionError("down")

    async def getdel(self, *args, **kwargs):
        raise ConnectionError("down")


async def test_redis_keeps_a_ticket_with_its_lifetime_and_spends_it_once():
    clock = Clock()
    store = RedisStore("redis://unused", "test:ticket", TICKET_LIFETIME_SECONDS, clock)
    fake = FakeRedis()
    store._client = fake
    ticket = await store.issue(grant(clock, connector_id=12))
    [(key, ttl)] = fake.ttl.items()
    assert key.startswith("test:ticket:") and ticket not in key
    assert ttl == TICKET_LIFETIME_SECONDS
    assert (await store.redeem(ticket)).connector_id == 12
    assert await store.redeem(ticket) is None


async def test_a_redis_entry_read_late_is_refused():
    clock = Clock()
    store = RedisStore("redis://unused", "test:code", CODE_LIFETIME_SECONDS, clock)
    store._client = FakeRedis()
    code = await store.issue(grant(clock))
    clock.now += CODE_LIFETIME_SECONDS + 1
    assert await store.redeem(code) is None


async def test_with_redis_down_this_worker_keeps_the_code():
    clock = Clock()
    store = RedisStore("redis://unused", "test:code", CODE_LIFETIME_SECONDS, clock)
    store._client = DownRedis()
    code = await store.issue(grant(clock))
    assert (await store.redeem(code)).purpose == "drive_picker"
    assert await store.redeem(code) is None
