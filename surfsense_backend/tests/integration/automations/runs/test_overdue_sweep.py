"""The overdue sweep: runs whose worker is gone, and queues that stalled."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest

import app.automations.concurrency.handoff as handoff_mod
import app.automations.runtime.overdue as overdue_mod
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.automation import Automation
from app.automations.persistence.models.run import AutomationRun
from app.automations.runtime.overdue import (
    OVERDUE_AFTER_SECONDS,
    restart_stalled_queues,
    time_out_overdue_runs,
)

from .conftest import definition

pytestmark = pytest.mark.integration

NOW = datetime.now(UTC)


@pytest.fixture
def spies(monkeypatch) -> dict[str, list]:
    seen: dict[str, list] = {"emails": [], "enqueued": []}

    async def email(_session, **kwargs):
        seen["emails"].append(kwargs)

    monkeypatch.setattr(overdue_mod, "send_run_result_email", email)
    monkeypatch.setattr(
        handoff_mod,
        "enqueue_run",
        lambda run_id, **_: seen["enqueued"].append(run_id),
    )
    return seen


async def _automation(db_session, db_workspace, db_user) -> Automation:
    automation = Automation(
        workspace_id=db_workspace.id,
        created_by_user_id=db_user.id,
        name="Nightly digest",
        definition=definition(),
    )
    db_session.add(automation)
    await db_session.flush()
    return automation


async def _run(
    db_session, automation, status, *, started=None, created=None, **execution
):
    run = AutomationRun(
        automation_id=automation.id,
        status=status,
        definition_snapshot=definition(**execution),
        inputs={},
        started_at=started,
    )
    if created is not None:
        run.created_at = created
    db_session.add(run)
    await db_session.flush()
    return run


async def test_a_running_run_past_its_limit_is_timed_out_emailed_and_handed_off(
    db_session, db_workspace, db_user, spies
):
    automation = await _automation(db_session, db_workspace, db_user)
    lost = await _run(
        db_session,
        automation,
        RunStatus.RUNNING,
        started=NOW - timedelta(seconds=60 + OVERDUE_AFTER_SECONDS + 5),
        timeout_seconds=60,
        email_result=True,
        concurrency="queue",
    )
    queued = await _run(db_session, automation, RunStatus.PENDING, concurrency="queue")

    swept = await time_out_overdue_runs(db_session, now=NOW)

    await db_session.refresh(lost)
    assert swept == [lost.id]
    assert lost.status == RunStatus.TIMED_OUT
    assert lost.error["type"] == "TimedOut"
    assert "1 minute" in lost.error["message"]
    assert [e["status"] for e in spies["emails"]] == ["timed_out"]
    assert spies["emails"][0]["automation_name"] == "Nightly digest"
    assert spies["enqueued"] == [queued.id]


async def test_a_run_still_within_its_limit_is_left_alone(
    db_session, db_workspace, db_user, spies
):
    automation = await _automation(db_session, db_workspace, db_user)
    run = await _run(
        db_session,
        automation,
        RunStatus.RUNNING,
        started=NOW - timedelta(seconds=60 + OVERDUE_AFTER_SECONDS - 30),
        timeout_seconds=60,
    )

    assert await time_out_overdue_runs(db_session, now=NOW) == []
    await db_session.refresh(run)
    assert run.status == RunStatus.RUNNING


async def test_a_long_stale_run_is_marked_without_an_email(
    db_session, db_workspace, db_user, spies
):
    automation = await _automation(db_session, db_workspace, db_user)
    run = await _run(
        db_session,
        automation,
        RunStatus.RUNNING,
        started=NOW - timedelta(days=3),
        timeout_seconds=600,
        email_result=True,
    )

    await time_out_overdue_runs(db_session, now=NOW)

    await db_session.refresh(run)
    assert run.status == RunStatus.TIMED_OUT
    assert spies["emails"] == []


async def test_a_stalled_queue_is_restarted(db_session, db_workspace, db_user, spies):
    automation = await _automation(db_session, db_workspace, db_user)
    stalled = await _run(
        db_session, automation, RunStatus.PENDING, created=NOW - timedelta(minutes=10)
    )

    assert await restart_stalled_queues(db_session, now=NOW) == [stalled.id]
    assert spies["enqueued"] == [stalled.id]


async def test_a_queue_behind_a_running_run_or_freshly_waiting_is_not_restarted(
    db_session, db_workspace, db_user, spies
):
    behind = await _automation(db_session, db_workspace, db_user)
    await _run(db_session, behind, RunStatus.RUNNING, started=NOW)
    await _run(
        db_session, behind, RunStatus.PENDING, created=NOW - timedelta(minutes=10)
    )
    fresh = await _automation(db_session, db_workspace, db_user)
    await _run(db_session, fresh, RunStatus.PENDING, created=NOW - timedelta(minutes=1))

    assert await restart_stalled_queues(db_session, now=NOW) == []
    assert spies["enqueued"] == []


# -- A run stranded while nothing could start it is not run late -------------


async def test_a_pending_run_stranded_for_days_is_cancelled_not_run(
    db_session, db_workspace, db_user, spies
):
    """E.g. a run whose message was lost before this sweep was deployed."""
    automation = await _automation(db_session, db_workspace, db_user)
    stranded = await _run(
        db_session, automation, RunStatus.PENDING, created=NOW - timedelta(days=8)
    )

    assert await restart_stalled_queues(db_session, now=NOW) == []

    await db_session.refresh(stranded)
    assert spies["enqueued"] == []
    assert stranded.status == RunStatus.CANCELLED
    assert stranded.error["type"] == "Cancelled"
    assert stranded.error["message"].startswith("Not started:")
    assert "1 hour" in stranded.error["message"]


async def test_a_run_lost_within_the_hour_is_still_re_sent(
    db_session, db_workspace, db_user, spies
):
    automation = await _automation(db_session, db_workspace, db_user)
    lost = await _run(
        db_session, automation, RunStatus.PENDING, created=NOW - timedelta(minutes=55)
    )

    assert await restart_stalled_queues(db_session, now=NOW) == [lost.id]
    assert spies["enqueued"] == [lost.id]


async def test_stranded_runs_are_dropped_and_a_fresh_one_behind_them_starts(
    db_session, db_workspace, db_user, spies
):
    automation = await _automation(db_session, db_workspace, db_user)
    old = [
        await _run(
            db_session,
            automation,
            RunStatus.PENDING,
            created=NOW - timedelta(days=days),
            concurrency="queue",
        )
        for days in (3, 2)
    ]
    fresh = await _run(
        db_session,
        automation,
        RunStatus.PENDING,
        created=NOW - timedelta(minutes=10),
        concurrency="queue",
    )

    assert await restart_stalled_queues(db_session, now=NOW) == [fresh.id]

    assert spies["enqueued"] == [fresh.id]
    for run in old:
        await db_session.refresh(run)
        assert run.status == RunStatus.CANCELLED


async def test_a_queued_run_that_waited_hours_behind_long_runs_still_starts(
    db_session, db_workspace, db_user, spies
):
    """Under "Queue the next run" the window counts from when the queue last
    moved, not from the fire."""
    automation = await _automation(db_session, db_workspace, db_user)
    ahead = await _run(
        db_session,
        automation,
        RunStatus.SUCCEEDED,
        started=NOW - timedelta(hours=3),
        concurrency="queue",
        timeout_seconds=86_400,
    )
    ahead.finished_at = NOW - timedelta(minutes=10)
    waited = await _run(
        db_session,
        automation,
        RunStatus.PENDING,
        created=NOW - timedelta(hours=3),
        concurrency="queue",
    )
    await db_session.flush()

    assert await restart_stalled_queues(db_session, now=NOW) == [waited.id]


async def test_runs_left_running_and_waiting_since_before_a_deploy_are_not_run_late(
    db_session, db_workspace, db_user, spies
):
    """Timing out the old run must not hand its days-old queue to a worker:
    the queue counts as having moved at that run's limit, not at the sweep."""
    automation = await _automation(db_session, db_workspace, db_user)
    lost = await _run(
        db_session,
        automation,
        RunStatus.RUNNING,
        started=NOW - timedelta(days=3),
        concurrency="queue",
    )
    behind = await _run(
        db_session,
        automation,
        RunStatus.PENDING,
        created=NOW - timedelta(days=3),
        concurrency="queue",
    )

    assert await time_out_overdue_runs(db_session, now=NOW) == [lost.id]

    await db_session.refresh(behind)
    assert spies["enqueued"] == []
    assert behind.status == RunStatus.CANCELLED
    assert behind.error["message"].startswith("Not started:")


# -- One odd stored limit cannot break the sweep -------------------------------


async def test_huge_or_damaged_stored_limits_do_not_break_the_sweep(
    db_session, db_workspace, db_user, spies
):
    """Stored before the 24-hour cap, or damaged: read as 24 hours or the
    default, never an error that would stop the sweep for every workspace."""
    automation = await _automation(db_session, db_workspace, db_user)

    async def running(age: timedelta, timeout) -> AutomationRun:
        return await _run(
            db_session,
            automation,
            RunStatus.RUNNING,
            started=NOW - age,
            concurrency="always",
            timeout_seconds=timeout,
        )

    huge_and_overdue = await running(timedelta(days=2), 3_000_000_000)
    huge_within_a_day = await running(timedelta(hours=2), 3_000_000_000)
    near_int_max = await running(timedelta(days=2), 2**31 - 100)
    not_a_number = await running(timedelta(hours=1), "ten minutes")
    ordinary = await running(timedelta(hours=1), 600)

    swept = await time_out_overdue_runs(db_session, now=NOW)

    assert sorted(swept) == sorted(
        [huge_and_overdue.id, near_int_max.id, not_a_number.id, ordinary.id]
    )
    await db_session.refresh(huge_within_a_day)
    assert huge_within_a_day.status == RunStatus.RUNNING
    await db_session.refresh(huge_and_overdue)
    assert "24 hours" in huge_and_overdue.error["message"]
