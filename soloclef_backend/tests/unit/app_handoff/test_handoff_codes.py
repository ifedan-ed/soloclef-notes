"""Hand-off codes as the connector sign-ins use them (MemoryHandoffCodes, RedisHandoffCodes): spent once, gone after 60 s, never stored as they are."""

from __future__ import annotations

import json

import pytest

from app.app_handoff.codes import (
    CODE_LIFETIME_SECONDS,
    DRIVE_CONNECT,
    HandoffGrant,
    MemoryHandoffCodes,
    RedisHandoffCodes,
)

pytestmark = pytest.mark.unit


class Clock:
    def __init__(self) -> None:
        self.now = 1_000_000.0

    def __call__(self) -> float:
        return self.now


def grant(clock: Clock, **extra) -> HandoffGrant:
    return HandoffGrant(
        purpose=DRIVE_CONNECT,
        user_id="u-1",
        workspace_id=7,
        account_id=None,
        issued_at=clock(),
        **extra,
    )


class FakeRedis:
    """``SET … EX NX`` and ``GETDEL``, as redis.asyncio answers them."""

    def __init__(self, broken: bool = False) -> None:
        self.data: dict[str, str] = {}
        self.broken = broken

    async def set(self, key, value, ex=None, nx=False):
        if self.broken:
            raise ConnectionError("redis down")
        assert ex == CODE_LIFETIME_SECONDS
        if nx and key in self.data:
            return None
        self.data[key] = value
        return True

    async def getdel(self, key):
        if self.broken:
            raise ConnectionError("redis down")
        return self.data.pop(key, None)


async def test_a_code_is_spent_once():
    clock = Clock()
    codes = MemoryHandoffCodes(clock)
    code = await codes.issue(grant(clock))
    assert len(code) >= 40
    assert (await codes.redeem(code)) == grant(clock)
    assert (await codes.redeem(code)) is None


async def test_a_code_runs_out_after_sixty_seconds():
    clock = Clock()
    codes = MemoryHandoffCodes(clock)
    code = await codes.issue(grant(clock))
    clock.now += CODE_LIFETIME_SECONDS
    assert (await codes.redeem(code)) is None


async def test_nothing_unknown_or_empty_redeems():
    codes = MemoryHandoffCodes(Clock())
    assert (await codes.redeem("")) is None
    assert (await codes.redeem("never-issued")) is None


async def test_redis_keeps_the_hash_never_the_code_and_spends_it_once():
    clock = Clock()
    redis = FakeRedis()
    codes = RedisHandoffCodes("redis://unused", clock=clock, client=redis)
    code = await codes.issue(grant(clock))
    [key] = redis.data
    assert code not in key
    assert code not in redis.data[key]
    assert json.loads(redis.data[key])["purpose"] == DRIVE_CONNECT
    assert (await codes.redeem(code)) == grant(clock)
    assert (await codes.redeem(code)) is None


async def test_redis_refuses_an_old_grant_even_if_it_is_still_there():
    clock = Clock()
    codes = RedisHandoffCodes("redis://unused", clock=clock, client=FakeRedis())
    code = await codes.issue(grant(clock))
    clock.now += CODE_LIFETIME_SECONDS + 1
    assert (await codes.redeem(code)) is None


async def test_without_redis_this_worker_keeps_the_codes():
    clock = Clock()
    codes = RedisHandoffCodes(
        "redis://unused", clock=clock, client=FakeRedis(broken=True)
    )
    code = await codes.issue(grant(clock))
    assert (await codes.redeem(code)) == grant(clock)
    assert (await codes.redeem(code)) is None
