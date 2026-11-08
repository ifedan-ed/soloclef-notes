"""The overdue sweep: runs no worker will finish, and queues that stalled.

Celery beat runs it every minute (``app/automations/tasks/sweep_overdue_runs.py``).

1. **Overdue runs.** A run still Running ``OVERDUE_AFTER_SECONDS`` past its
   own time limit has lost its worker -- restarted for a deploy, killed by
   Celery's backstop limit, or crashed -- because a live worker stops it at
   the limit itself. It is marked Timed out, its result email goes out when
   "Email me the result" is on, and the next queued run is started. Runs
   whose limit passed more than ``EMAIL_WINDOW`` ago (left over from before
   this sweep existed) are marked without an email.
2. **Stalled queues.** An automation with a run Pending for ``STALLED_AFTER``
   and nothing Running gets its oldest pending run handed to a worker again
   (its message was lost, or the wake-up after the previous run failed) --
   unless it is past its start window (an hour, ``concurrency/handoff.py``):
   then it is Cancelled as "Not started" instead of running late.

The two passes run separately (``tasks/sweep_overdue_runs.py``), so an error
in one never stops the other; and a run's time limit is read from its
snapshot by ``snapshot_timeout_sql``, which cannot fail on a stored value.
"""

from __future__ import annotations

import logging
from datetime import datetime, timedelta

from sqlalchemy import exists, func, literal_column, select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import aliased, selectinload

from app.automations.concurrency.handoff import start_next_waiting_run
from app.automations.dispatch.enqueue import OVERDUE_AFTER_SECONDS
from app.automations.notify import send_run_result_email
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.run import AutomationRun
from app.push import events as push_events

from . import repository
from .time_limit import (
    run_overdue_error,
    snapshot_timeout_seconds,
    snapshot_timeout_sql,
)

logger = logging.getLogger(__name__)

# A run still Running OVERDUE_AFTER_SECONDS (180) past its own limit is lost:
# the worker's backstop kill plus a minute (defined in dispatch/enqueue.py).
# How long a run may sit Pending, with nothing running, before it is re-sent.
STALLED_AFTER = timedelta(minutes=5)
# Overdue runs whose limit passed longer ago than this get no email.
EMAIL_WINDOW = timedelta(hours=1)
# Rows handled per sweep; the rest are picked up a minute later.
_BATCH = 100


async def time_out_overdue_runs(session: AsyncSession, *, now: datetime) -> list[int]:
    """Mark lost Running runs Timed out; email and hand off. Returns their ids."""
    timeout = snapshot_timeout_sql(AutomationRun.definition_snapshot)
    started = func.coalesce(AutomationRun.started_at, AutomationRun.created_at)
    overdue_at = started + (timeout + OVERDUE_AFTER_SECONDS) * literal_column(
        "interval '1 second'"
    )
    runs = (
        (
            await session.execute(
                select(AutomationRun)
                .where(AutomationRun.status == RunStatus.RUNNING, overdue_at < now)
                .order_by(AutomationRun.id)
                .limit(_BATCH)
                .options(selectinload(AutomationRun.automation))
            )
        )
        .scalars()
        .all()
    )

    swept: list[AutomationRun] = []
    for run in runs:
        limit = snapshot_timeout_seconds(run.definition_snapshot)
        ended = await repository.finish(
            session, run.id, RunStatus.TIMED_OUT, error=run_overdue_error(limit)
        )
        # One run row at a time: never holding two run rows, the sweep cannot
        # deadlock with a Pause cancelling the same runs in another order.
        await session.commit()
        if ended:
            swept.append(run)

    for run in swept:
        logger.warning(
            "automation_run %d (automation %d) lost its worker; marked timed out",
            run.id,
            run.automation_id,
        )
        # The phone: "An automation failed" (timed out), to its owner (app/push).
        push_events.automation_run_ended(
            owner_user_id=run.automation.created_by_user_id if run.automation else None,
            automation_id=run.automation_id,
            run_id=run.id,
            status=RunStatus.TIMED_OUT.value,
        )
        await _email_if_recent(session, run, now=now)
        await start_next_waiting_run(session, run.automation_id, now=now)
    return [run.id for run in swept]


async def restart_stalled_queues(session: AsyncSession, *, now: datetime) -> list[int]:
    """Re-send the oldest pending run of automations whose queue stopped moving.

    Runs past their start window are Cancelled as "Not started" instead
    (``start_next_waiting_run``). Returns the ids handed to a worker.
    """
    running = aliased(AutomationRun)
    automation_ids = (
        (
            await session.execute(
                select(AutomationRun.automation_id)
                .where(
                    AutomationRun.status == RunStatus.PENDING,
                    AutomationRun.created_at < now - STALLED_AFTER,
                    ~exists().where(
                        running.automation_id == AutomationRun.automation_id,
                        running.status == RunStatus.RUNNING,
                    ),
                )
                .distinct()
                .limit(_BATCH)
            )
        )
        .scalars()
        .all()
    )
    restarted: list[int] = []
    for automation_id in automation_ids:
        run_id = await start_next_waiting_run(session, automation_id, now=now)
        if run_id is not None:
            logger.warning(
                "automation %d: pending run %d had stalled; handed to a worker again",
                automation_id,
                run_id,
            )
            restarted.append(run_id)
    return restarted


async def _email_if_recent(
    session: AsyncSession, run: AutomationRun, *, now: datetime
) -> None:
    snapshot = (
        run.definition_snapshot if isinstance(run.definition_snapshot, dict) else {}
    )
    execution = snapshot.get("execution") or {}
    automation = run.automation
    if not execution.get("email_result") or automation is None:
        return
    started = run.started_at or run.created_at
    deadline = started + timedelta(seconds=snapshot_timeout_seconds(snapshot))
    if now - deadline > EMAIL_WINDOW:
        return
    try:
        await send_run_result_email(
            session,
            owner_user_id=automation.created_by_user_id,
            automation_id=automation.id,
            automation_name=automation.name,
            status=RunStatus.TIMED_OUT.value,
            error=run_overdue_error(snapshot_timeout_seconds(snapshot)),
        )
    except Exception:
        # Best-effort, like every result email; the next run must still start.
        logger.exception("automation_run %d: timed-out email not sent", run.id)
