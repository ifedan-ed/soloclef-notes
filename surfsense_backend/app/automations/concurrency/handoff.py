"""Wake the next waiting run once an automation has nothing running.

Called after a run ends (the executor, the overdue sweep), after a person
cancels a run (``services/run.py``), and by the sweep for a queue that has sat
still too long. It only hands the oldest pending run to a worker; whether that
run may really start is decided again under the lock by ``start.py``, so
calling this twice, or while something is still running, is harmless.

**Start window.** A waiting run is handed to a worker only within
``START_WINDOW`` of the moment it could first have started:

* under "Skip if already running" and "Always run", when it was created --
  such a run starts within seconds unless its message was lost;
* under "Queue the next run", the later of that and when the queue last
  moved: the latest end of a run of the automation that did start, counted at
  most ``OVERDUE_AFTER_SECONDS`` past that run's time limit (a run that went on
  longer had lost its worker and was only marked Timed out later, perhaps
  days later at a deploy).

A run past its window was stranded while nothing could start it (its message
lost before a deploy, or the sweep not running). It is marked Cancelled with
"Not started: ..." instead of running hours or days late on the inputs of a
long-gone fire, with the credits, note and email that would bring; the next
waiting run is then considered the same way.
"""

from __future__ import annotations

import contextlib
import logging
from datetime import UTC, datetime, timedelta
from typing import Any

from sqlalchemy import exists, func, literal_column, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.dispatch.enqueue import OVERDUE_AFTER_SECONDS, enqueue_run
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.run import AutomationRun
from app.automations.runtime import repository
from app.automations.runtime.cancellation import not_started_error
from app.automations.runtime.time_limit import (
    describe_seconds,
    snapshot_timeout_seconds,
    snapshot_timeout_sql,
)

from .lock import lock_automation

logger = logging.getLogger(__name__)

# How long after it could first have started a waiting run may still be
# handed to a worker (see the module docstring). The sweep re-sends a lost
# run after 5 minutes, so only a run stranded while nothing was watching
# reaches this.
START_WINDOW = timedelta(hours=1)
# Waiting runs looked at per call; more stranded ones than this are cleared
# on the next call (a queue holds at most QUEUE_LIMIT, 20).
_SCAN = 100


async def start_next_waiting_run(
    session: AsyncSession,
    automation_id: int,
    *,
    now: datetime | None = None,
) -> int | None:
    """Enqueue the automation's oldest pending run if none is running.

    Pending runs ahead of it that are past their start window are Cancelled
    first (and committed). Returns the run id handed off, or ``None``. Call
    it after committing: on a database error it rolls the session back.
    Never raises: a failed wake-up is retried by the overdue sweep's
    stalled-queue pass.
    """
    now = now or datetime.now(UTC)
    try:
        running = (
            await session.execute(
                select(
                    exists().where(
                        AutomationRun.automation_id == automation_id,
                        AutomationRun.status == RunStatus.RUNNING,
                    )
                )
            )
        ).scalar()
        if running:
            return None
        waiting = (
            await session.execute(
                select(
                    AutomationRun.id,
                    AutomationRun.created_at,
                    AutomationRun.definition_snapshot,
                )
                .where(
                    AutomationRun.automation_id == automation_id,
                    AutomationRun.status == RunStatus.PENDING,
                )
                .order_by(AutomationRun.id)
                .limit(_SCAN)
            )
        ).all()
        if not waiting:
            return None

        queue_moved_at: datetime | None = None
        if any(_policy(row.definition_snapshot) == "queue" for row in waiting):
            queue_moved_at = await _queue_moved_at(session, automation_id)

        stranded: list[int] = []
        next_run = None
        for row in waiting:
            ready_at = row.created_at
            if _policy(row.definition_snapshot) == "queue" and queue_moved_at:
                ready_at = max(ready_at, queue_moved_at)
            if now - ready_at <= START_WINDOW:
                next_run = row
                break
            stranded.append(row.id)

        if stranded:
            await _drop_stranded(session, automation_id, stranded)
        if next_run is None:
            return None
        enqueue_run(
            next_run.id,
            timeout_seconds=snapshot_timeout_seconds(next_run.definition_snapshot),
        )
        return next_run.id
    except Exception:
        logger.exception(
            "automation %d: could not hand the next queued run to a worker",
            automation_id,
        )
        with contextlib.suppress(Exception):
            await session.rollback()
        return None


async def _drop_stranded(
    session: AsyncSession, automation_id: int, run_ids: list[int]
) -> None:
    """Cancel stranded pending runs as "Not started", then commit.

    Under the automation's row lock, taken before any run row as pausing and
    claiming a run do, so this cannot deadlock with either. Each write moves
    a run from Pending only: one a worker started meanwhile is left alone.
    """
    if await lock_automation(session, automation_id) is None:
        await session.rollback()
        return
    error = not_started_error(describe_seconds(int(START_WINDOW.total_seconds())))
    for run_id in run_ids:
        if await repository.finish(
            session,
            run_id,
            RunStatus.CANCELLED,
            error=error,
            from_statuses=(RunStatus.PENDING,),
        ):
            logger.warning(
                "automation_run %d (automation %d) waited over %s to start; "
                "cancelled as not started rather than run late",
                run_id,
                automation_id,
                START_WINDOW,
            )
    await session.commit()


async def _queue_moved_at(session: AsyncSession, automation_id: int) -> datetime | None:
    """When the automation's last started run stopped holding the queue.

    Its ``finished_at``, but no later than ``OVERDUE_AFTER_SECONDS`` past its
    own time limit. ``None`` if no run of it has started and finished.
    """
    runs = AutomationRun
    held_until = runs.started_at + (
        snapshot_timeout_sql(runs.definition_snapshot) + OVERDUE_AFTER_SECONDS
    ) * literal_column("interval '1 second'")
    return (
        await session.execute(
            select(func.max(func.least(runs.finished_at, held_until))).where(
                runs.automation_id == automation_id,
                runs.started_at.is_not(None),
                runs.finished_at.is_not(None),
            )
        )
    ).scalar()


def _policy(snapshot: Any) -> str:
    """``execution.concurrency`` of a run's snapshot (default: skip if running)."""
    execution = snapshot.get("execution") if isinstance(snapshot, dict) else None
    value = execution.get("concurrency") if isinstance(execution, dict) else None
    return value if isinstance(value, str) else "drop_if_running"
