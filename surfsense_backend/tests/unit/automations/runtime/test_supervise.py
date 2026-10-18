"""``supervise``: a plan ends on its own, at its time limit, or when stopped."""

from __future__ import annotations

import asyncio

import pytest

from app.automations.runtime.supervise import Ending, supervise

pytestmark = pytest.mark.unit


async def test_a_plan_that_finishes_returns_its_value():
    async def plan():
        return "done"

    supervised = await supervise(plan(), time_left=5)

    assert supervised.ending is Ending.FINISHED
    assert supervised.value == "done"


async def test_a_plan_that_raises_reraises():
    async def plan():
        raise RuntimeError("boom")

    with pytest.raises(RuntimeError, match="boom"):
        await supervise(plan(), time_left=5)


async def test_a_plan_past_its_time_limit_is_cancelled_mid_step():
    cancelled = asyncio.Event()

    async def plan():
        try:
            await asyncio.sleep(60)
        except asyncio.CancelledError:
            cancelled.set()
            raise

    supervised = await supervise(plan(), time_left=0.05)

    assert supervised.ending is Ending.TIMED_OUT
    assert cancelled.is_set()


async def test_a_stop_request_cancels_the_plan():
    cancelled = asyncio.Event()
    stop = {"now": False}

    async def plan():
        try:
            stop["now"] = True
            await asyncio.sleep(60)
        except asyncio.CancelledError:
            cancelled.set()
            raise

    async def should_stop():
        return stop["now"]

    supervised = await supervise(
        plan(), time_left=30, should_stop=should_stop, poll_seconds=0.01
    )

    assert supervised.ending is Ending.STOPPED
    assert cancelled.is_set()


async def test_a_failing_stop_check_is_retried_not_fatal():
    calls = {"n": 0}

    async def should_stop():
        calls["n"] += 1
        if calls["n"] == 1:
            raise ConnectionError("db hiccup")
        return calls["n"] >= 3

    async def plan():
        await asyncio.sleep(60)

    supervised = await supervise(
        plan(), time_left=30, should_stop=should_stop, poll_seconds=0.01
    )

    assert supervised.ending is Ending.STOPPED
    assert calls["n"] == 3


async def test_a_finished_plan_stops_watching():
    checks = {"n": 0}

    async def should_stop():
        checks["n"] += 1
        return False

    async def plan():
        await asyncio.sleep(0.05)
        return 1

    supervised = await supervise(
        plan(), time_left=30, should_stop=should_stop, poll_seconds=0.01
    )
    seen = checks["n"]
    await asyncio.sleep(0.05)

    assert supervised.ending is Ending.FINISHED
    assert checks["n"] == seen
