"""A run that hits its time limit ends "Timed out", stops its step, and emails."""

from __future__ import annotations

import pytest

import app.automations.concurrency.handoff as handoff_mod
import app.automations.runtime.executor as executor_mod
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.runtime.executor import execute_run

pytestmark = pytest.mark.integration


@pytest.fixture
def emails(monkeypatch) -> list[dict]:
    sent: list[dict] = []

    async def capture(_session, **kwargs):
        sent.append(kwargs)

    monkeypatch.setattr(executor_mod, "send_run_result_email", capture)
    monkeypatch.setattr(handoff_mod, "enqueue_run", lambda *a, **k: None)
    return sent


async def _execute(world, run_id: int) -> None:
    async with world.sessions() as session:
        await execute_run(session, run_id)


async def test_a_run_past_its_time_limit_is_timed_out_and_emailed(world, gate, emails):
    automation_id, _ = await world.automation()
    run_id = await world.pending_run(
        automation_id, timeout_seconds=1, step_timeout=30, email_result=True
    )

    await _execute(world, run_id)

    run = await world.run(run_id)
    assert run.status == RunStatus.TIMED_OUT
    assert run.finished_at is not None
    assert run.error["type"] == "TimedOut"
    assert "1 second" in run.error["message"]
    # The step in flight was stopped, and the run page says where.
    assert gate.cancelled.is_set()
    assert run.step_results[-1]["status"] == "timed_out"
    assert run.step_results[-1]["step_id"] == "step_1"
    assert [e["status"] for e in emails] == ["timed_out"]
    assert emails[0]["error"]["type"] == "TimedOut"


async def test_a_step_past_its_own_limit_times_the_run_out(world, gate, emails):
    automation_id, _ = await world.automation()
    run_id = await world.pending_run(
        automation_id, timeout_seconds=30, step_timeout=1, email_result=True
    )

    await _execute(world, run_id)

    run = await world.run(run_id)
    assert run.status == RunStatus.TIMED_OUT
    assert run.error["type"] == "StepTimedOut"
    assert run.step_results[-1]["status"] == "timed_out"
    assert [e["status"] for e in emails] == ["timed_out"]


async def test_no_email_when_email_me_the_result_is_off(world, gate, emails):
    automation_id, _ = await world.automation()
    run_id = await world.pending_run(automation_id, timeout_seconds=1, step_timeout=30)

    await _execute(world, run_id)

    assert (await world.run(run_id)).status == RunStatus.TIMED_OUT
    assert emails == []


async def test_a_run_that_finishes_in_time_still_succeeds(world, gate, emails):
    automation_id, _ = await world.automation()
    run_id = await world.pending_run(automation_id, email_result=True)
    gate.release.set()

    await _execute(world, run_id)

    run = await world.run(run_id)
    assert run.status == RunStatus.SUCCEEDED
    assert run.error is None
    assert [e["status"] for e in emails] == ["succeeded"]
