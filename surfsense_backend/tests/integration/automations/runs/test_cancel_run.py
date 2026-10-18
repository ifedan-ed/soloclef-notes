"""Cancelling a run: a person's Cancel, pausing, or deleting during a run."""

from __future__ import annotations

import asyncio

import pytest
from fastapi import HTTPException
from sqlalchemy import text

import app.automations.concurrency.handoff as handoff_mod
import app.automations.runtime.executor as executor_mod
import app.automations.runtime.repository as repository_mod
import app.automations.runtime.supervise as supervise_mod
import app.automations.services.automation as automation_service_mod
from app.auth.context import AuthContext
from app.automations.persistence.enums.automation_status import AutomationStatus
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.automation import Automation
from app.automations.persistence.models.run import AutomationRun
from app.automations.runtime.executor import execute_run
from app.automations.runtime.stop_probe import run_ended_elsewhere
from app.automations.schemas.api import AutomationUpdate
from app.automations.services import AutomationService, RunService

pytestmark = pytest.mark.integration


@pytest.fixture
def emails(monkeypatch) -> list[dict]:
    sent: list[dict] = []

    async def capture(_session, **kwargs):
        sent.append(kwargs)

    monkeypatch.setattr(executor_mod, "send_run_result_email", capture)
    monkeypatch.setattr(handoff_mod, "enqueue_run", lambda *a, **k: None)
    # Check for a cancel often, so the test doesn't wait the real 5 seconds.
    monkeypatch.setattr(supervise_mod, "STOP_POLL_SECONDS", 0.05)
    return sent


async def _start(world, run_id: int) -> asyncio.Task:
    """Execute like the worker does: own session, plus the status probe."""

    async def execute():
        async with world.sessions() as session:
            await execute_run(
                session,
                run_id,
                should_stop=run_ended_elsewhere(world.sessions, run_id),
            )

    return asyncio.create_task(execute())


async def test_cancelling_a_running_run_stops_it(world, gate, emails):
    automation_id, _ = await world.automation(email_result=True)
    run_id = await world.pending_run(automation_id, email_result=True)
    worker = await _start(world, run_id)
    await asyncio.wait_for(gate.started.wait(), timeout=10)

    async with world.sessions() as session:
        returned = await RunService(
            session=session, auth=AuthContext.session(world.user)
        ).cancel(automation_id=automation_id, run_id=run_id)
    assert returned.status == RunStatus.CANCELLED

    await asyncio.wait_for(worker, timeout=10)

    run = await world.run(run_id)
    assert gate.cancelled.is_set()
    assert run.status == RunStatus.CANCELLED
    assert run.error == {
        "type": "Cancelled",
        "message": "Cancelled before it finished.",
    }
    assert run.step_results[-1]["status"] == "cancelled"
    assert emails == []  # the person who cancelled already knows


async def test_a_cancelled_pending_run_never_starts(world, gate, emails):
    automation_id, _ = await world.automation()
    run_id = await world.pending_run(automation_id)

    async with world.sessions() as session:
        await RunService(session=session, auth=AuthContext.session(world.user)).cancel(
            automation_id=automation_id, run_id=run_id
        )
    worker = await _start(world, run_id)
    await asyncio.wait_for(worker, timeout=10)

    assert gate.calls == 0
    assert (await world.run(run_id)).status == RunStatus.CANCELLED


async def test_cancelling_a_finished_run_is_refused(world, gate, emails):
    automation_id, _ = await world.automation()
    run_id = await world.pending_run(automation_id)
    gate.release.set()
    await asyncio.wait_for(await _start(world, run_id), timeout=10)

    async with world.sessions() as session:
        service = RunService(session=session, auth=AuthContext.session(world.user))
        with pytest.raises(HTTPException) as exc_info:
            await service.cancel(automation_id=automation_id, run_id=run_id)

    assert exc_info.value.status_code == 409
    assert (await world.run(run_id)).status == RunStatus.SUCCEEDED


async def test_pausing_during_a_run_cancels_it_and_what_waits(world, gate, emails):
    automation_id, _ = await world.automation(concurrency="queue")
    running_id = await world.pending_run(automation_id, concurrency="queue")
    worker = await _start(world, running_id)
    await asyncio.wait_for(gate.started.wait(), timeout=10)
    waiting_id = await world.pending_run(automation_id, concurrency="queue")

    async with world.sessions() as session:
        await AutomationService(
            session=session, auth=AuthContext.session(world.user)
        ).update(automation_id, AutomationUpdate(status=AutomationStatus.PAUSED))
    await asyncio.wait_for(worker, timeout=10)

    paused = "Cancelled because the automation was paused."
    for run_id in (running_id, waiting_id):
        run = await world.run(run_id)
        assert run.status == RunStatus.CANCELLED
        assert run.error["message"] == paused
    assert gate.cancelled.is_set()


async def test_a_run_of_a_paused_automation_is_cancelled_not_started(
    world, gate, emails
):
    """Paused between the fire and the worker picking the run up."""
    automation_id, _ = await world.automation()
    run_id = await world.pending_run(automation_id)
    async with world.sessions() as session:
        automation = await session.get(Automation, automation_id)
        automation.status = AutomationStatus.PAUSED
        await session.commit()

    await asyncio.wait_for(await _start(world, run_id), timeout=10)

    run = await world.run(run_id)
    assert gate.calls == 0
    assert run.status == RunStatus.CANCELLED
    assert run.error["message"] == "Cancelled because the automation was paused."


async def test_deleting_during_a_run_stops_it_quietly(world, gate, emails):
    automation_id, _ = await world.automation()
    run_id = await world.pending_run(automation_id)
    worker = await _start(world, run_id)
    await asyncio.wait_for(gate.started.wait(), timeout=10)

    async with world.sessions() as session:
        await AutomationService(
            session=session, auth=AuthContext.session(world.user)
        ).delete(automation_id)
    await asyncio.wait_for(worker, timeout=10)  # no exception escapes

    assert gate.cancelled.is_set()
    async with world.sessions() as session:
        assert await session.get(AutomationRun, run_id) is None
    assert emails == []


# -- Pause and a worker starting a run at the same moment --------------------
#
# Both take the automation's row lock before touching run rows. These hold one
# side at its most dangerous point until the other is blocked on a lock, then
# let both go: in the opposite order Postgres would find a deadlock and abort
# one of them (the Pause as a 500, or the worker as a crashed task).


async def _pause(world, automation_id: int) -> None:
    async with world.sessions() as session:
        await AutomationService(
            session=session, auth=AuthContext.session(world.user)
        ).update(automation_id, AutomationUpdate(status=AutomationStatus.PAUSED))


async def _until_a_connection_waits_on_a_lock(world) -> None:
    for _ in range(500):
        async with world.sessions() as session:
            waiting = (
                await session.execute(
                    text("SELECT EXISTS (SELECT 1 FROM pg_locks WHERE NOT granted)")
                )
            ).scalar()
        if waiting:
            return
        await asyncio.sleep(0.01)
    raise AssertionError("neither side ever waited for the other")


async def _automation_status(world, automation_id: int) -> AutomationStatus:
    async with world.sessions() as session:
        return (await session.get(Automation, automation_id)).status


async def test_pause_then_a_worker_starting_the_run_do_not_deadlock(
    world, gate, emails, monkeypatch
):
    automation_id, _ = await world.automation()
    run_id = await world.pending_run(automation_id)
    gate.release.set()
    cancelled, go_on = asyncio.Event(), asyncio.Event()
    real_cancel = automation_service_mod.cancel_unfinished_runs

    async def cancel_then_hold(session, **kwargs):
        ids = await real_cancel(session, **kwargs)
        cancelled.set()
        await go_on.wait()
        return ids

    monkeypatch.setattr(
        automation_service_mod, "cancel_unfinished_runs", cancel_then_hold
    )

    pause = asyncio.create_task(_pause(world, automation_id))
    await asyncio.wait_for(cancelled.wait(), timeout=10)
    worker = await _start(world, run_id)
    await _until_a_connection_waits_on_a_lock(world)
    go_on.set()
    results = await asyncio.wait_for(
        asyncio.gather(pause, worker, return_exceptions=True), timeout=20
    )

    assert [r for r in results if isinstance(r, BaseException)] == []
    assert await _automation_status(world, automation_id) == AutomationStatus.PAUSED
    assert (await world.run(run_id)).status == RunStatus.CANCELLED
    assert gate.calls == 0


async def test_a_worker_starting_the_run_then_pause_do_not_deadlock(
    world, gate, emails, monkeypatch
):
    automation_id, _ = await world.automation()
    run_id = await world.pending_run(automation_id)
    claimed, go_on = asyncio.Event(), asyncio.Event()
    real_start = repository_mod.start_if_pending

    async def hold_then_start(session, run):
        claimed.set()  # the worker holds the automation lock here
        await go_on.wait()
        return await real_start(session, run)

    monkeypatch.setattr(repository_mod, "start_if_pending", hold_then_start)

    worker = await _start(world, run_id)
    await asyncio.wait_for(claimed.wait(), timeout=10)
    pause = asyncio.create_task(_pause(world, automation_id))
    await _until_a_connection_waits_on_a_lock(world)
    go_on.set()
    results = await asyncio.wait_for(
        asyncio.gather(pause, worker, return_exceptions=True), timeout=20
    )

    assert [r for r in results if isinstance(r, BaseException)] == []
    assert await _automation_status(world, automation_id) == AutomationStatus.PAUSED
    run = await world.run(run_id)
    assert run.status == RunStatus.CANCELLED
    assert run.error["message"] == "Cancelled because the automation was paused."
