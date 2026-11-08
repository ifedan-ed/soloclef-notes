""" "If already running" with two fires at once: skip, queue, always run.

Two fires race on separate sessions, the way two workers (or two triggers in
one tick) would. The admission check is slowed down so that without the
automation row lock both would count zero runs before either inserted one.
"""

from __future__ import annotations

import asyncio
from datetime import UTC, datetime, timedelta

import pytest
from sqlalchemy import update

import app.automations.concurrency.handoff as handoff_mod
import app.automations.dispatch.launch as launch_mod
from app.auth.context import AuthContext
from app.automations.dispatch import launch_run
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.enums.trigger_type import TriggerType
from app.automations.persistence.models.run import AutomationRun
from app.automations.persistence.models.trigger import AutomationTrigger
from app.automations.runtime.cancellation import cancel_unfinished_runs
from app.automations.runtime.executor import execute_run
from app.automations.services import RunService
from app.automations.triggers.builtin.schedule import selector

pytestmark = pytest.mark.integration


@pytest.fixture
def enqueued(monkeypatch) -> list[int]:
    """Run ids handed to a worker (launch and hand-off), without a broker."""
    calls: list[int] = []

    def spy(run_id: int, *, timeout_seconds: int) -> None:
        calls.append(run_id)

    monkeypatch.setattr(launch_mod, "enqueue_run", spy)
    monkeypatch.setattr(handoff_mod, "enqueue_run", spy)
    return calls


@pytest.fixture
def slow_admission(monkeypatch) -> None:
    real = launch_mod.admit_new_run

    async def slow(session, automation_id, policy):
        decision = await real(session, automation_id, policy)
        await asyncio.sleep(0.3)
        return decision

    monkeypatch.setattr(launch_mod, "admit_new_run", slow)


async def _fire_twice_at_once(world, trigger_id: int):
    async def fire():
        async with world.sessions() as session:
            trigger = await session.get(AutomationTrigger, trigger_id)
            return await launch_run(session=session, trigger=trigger, runtime_inputs={})

    return await asyncio.gather(fire(), fire())


async def test_skip_if_already_running_lets_one_of_two_fires_through(
    world, enqueued, slow_admission
):
    automation_id, trigger_id = await world.automation(concurrency="drop_if_running")

    results = await _fire_twice_at_once(world, trigger_id)

    created = [r for r in results if r is not None]
    assert len(created) == 1
    runs = await world.runs(automation_id)
    assert [r.id for r in runs] == [created[0].id]
    assert enqueued == [created[0].id]


async def test_always_run_lets_both_fires_through(world, enqueued, slow_admission):
    automation_id, trigger_id = await world.automation(concurrency="always")

    results = await _fire_twice_at_once(world, trigger_id)

    assert all(r is not None for r in results)
    assert len(await world.runs(automation_id)) == 2
    assert sorted(enqueued) == sorted(r.id for r in results)


async def test_always_run_runs_side_by_side(world, enqueued, gate):
    automation_id, _ = await world.automation(concurrency="always")
    first = await world.pending_run(automation_id, concurrency="always")
    second = await world.pending_run(automation_id, concurrency="always")

    async def execute(run_id):
        async with world.sessions() as session:
            await execute_run(session, run_id)

    tasks = [asyncio.create_task(execute(first)), asyncio.create_task(execute(second))]
    for _ in range(100):
        if gate.calls == 2:
            break
        await asyncio.sleep(0.05)
    assert gate.calls == 2  # both started before either finished
    gate.release.set()
    await asyncio.wait_for(asyncio.gather(*tasks), timeout=10)

    assert [r.status for r in await world.runs(automation_id)] == [
        RunStatus.SUCCEEDED,
        RunStatus.SUCCEEDED,
    ]


async def test_queue_runs_the_second_fire_after_the_first(
    world, enqueued, slow_admission, gate
):
    _automation_id, trigger_id = await world.automation(concurrency="queue")
    first, second = await _fire_twice_at_once(world, trigger_id)
    assert first is not None and second is not None
    first_id, second_id = sorted([first.id, second.id])

    async def execute(run_id):
        async with world.sessions() as session:
            await execute_run(session, run_id)

    running = asyncio.create_task(execute(first_id))
    await asyncio.wait_for(gate.started.wait(), timeout=10)

    # The queued run's worker arrives while the first is still going: it waits.
    await execute(second_id)
    assert (await world.run(second_id)).status == RunStatus.PENDING
    assert gate.calls == 1

    enqueued.clear()
    gate.release.set()
    await asyncio.wait_for(running, timeout=10)
    assert (await world.run(first_id)).status == RunStatus.SUCCEEDED
    # Finishing handed the waiting run to a worker...
    assert enqueued == [second_id]

    # ...which now starts and finishes it.
    await execute(second_id)
    assert (await world.run(second_id)).status == RunStatus.SUCCEEDED
    assert gate.calls == 2


async def test_a_queued_run_waits_behind_an_older_waiting_run(world, enqueued, gate):
    """Oldest first: a newer queued run does not jump an older one that has
    not started yet."""
    automation_id, _ = await world.automation(concurrency="queue")
    older = await world.pending_run(automation_id, concurrency="queue")
    newer = await world.pending_run(automation_id, concurrency="queue")

    async with world.sessions() as session:
        await execute_run(session, newer)

    assert (await world.run(newer)).status == RunStatus.PENDING
    assert (await world.run(older)).status == RunStatus.PENDING
    assert gate.calls == 0


async def test_two_due_schedule_fires_in_one_tick_start_one_run_when_skipping(
    world, enqueued
):
    """Two schedule triggers of one automation due in the same tick."""
    automation_id, first_trigger = await world.automation(concurrency="drop_if_running")
    past = datetime.now(UTC) - timedelta(minutes=1)
    async with world.sessions() as session:
        session.add(
            AutomationTrigger(
                automation_id=automation_id,
                type=TriggerType.SCHEDULE,
                params={"cron": "* * * * *", "timezone": "UTC"},
                static_inputs={},
                next_fire_at=past,
            )
        )
        trigger = await session.get(AutomationTrigger, first_trigger)
        trigger.next_fire_at = past
        await session.commit()

    await selector._tick()

    runs = await world.runs(automation_id)
    assert len(runs) == 1
    assert runs[0].status == RunStatus.PENDING
    assert enqueued == [runs[0].id]


# -- Cancelling a run passes the queue on at once ----------------------------


async def _cancel(world, automation_id: int, run_id: int) -> None:
    async with world.sessions() as session:
        await RunService(session=session, auth=AuthContext.session(world.user)).cancel(
            automation_id=automation_id, run_id=run_id
        )


async def test_cancelling_the_head_of_the_queue_starts_the_next_run(
    world, enqueued, gate
):
    automation_id, _ = await world.automation(concurrency="queue")
    head = await world.pending_run(automation_id, concurrency="queue")
    behind = await world.pending_run(automation_id, concurrency="queue")
    # The run behind's worker came first and is waiting behind the head.
    async with world.sessions() as session:
        await execute_run(session, behind)
    assert (await world.run(behind)).status == RunStatus.PENDING

    await _cancel(world, automation_id, head)

    assert enqueued == [behind]


async def test_cancelling_a_run_whose_worker_is_gone_starts_the_next_run(
    world, enqueued, gate
):
    automation_id, _ = await world.automation(concurrency="queue")
    lost = await world.pending_run(automation_id, concurrency="queue")
    async with world.sessions() as session:
        await session.execute(
            update(AutomationRun)
            .where(AutomationRun.id == lost)
            .values(status=RunStatus.RUNNING, started_at=datetime.now(UTC))
        )
        await session.commit()
    behind = await world.pending_run(automation_id, concurrency="queue")

    await _cancel(world, automation_id, lost)

    assert (await world.run(lost)).status == RunStatus.CANCELLED
    assert enqueued == [behind]


async def test_the_message_of_a_run_cancelled_while_waiting_passes_the_queue_on(
    world, enqueued, gate
):
    """Backs up Cancel's own hand-off, should that one fail."""
    automation_id, _ = await world.automation(concurrency="queue")
    head = await world.pending_run(automation_id, concurrency="queue")
    behind = await world.pending_run(automation_id, concurrency="queue")
    async with world.sessions() as session:
        await cancel_unfinished_runs(session, automation_id=automation_id, run_id=head)
        await session.commit()

    async with world.sessions() as session:
        await execute_run(session, head)  # its message arrives after the cancel

    assert gate.calls == 0
    assert enqueued == [behind]
