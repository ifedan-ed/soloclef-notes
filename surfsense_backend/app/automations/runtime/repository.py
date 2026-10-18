"""Persistence operations on ``AutomationRun``. Pure SQL, no business logic.

Status changes are compare-and-set: each one names the status it moves the
run *from*, and does nothing when the row has moved on. A person cancelling,
the overdue sweep, a duplicate worker message and the executor itself can all
reach the same run at once; whichever writes first wins and the others get
``False`` instead of overwriting it.
"""

from __future__ import annotations

from collections.abc import Iterable
from datetime import UTC, datetime
from typing import Any

from sqlalchemy import bindparam, select, update
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload
from sqlalchemy.orm.attributes import set_committed_value

from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.run import AutomationRun

_runs = AutomationRun.__table__


async def load_run(session: AsyncSession, run_id: int) -> AutomationRun | None:
    """Load a run with its automation and trigger eagerly loaded."""
    stmt = (
        select(AutomationRun)
        .where(AutomationRun.id == run_id)
        .options(
            selectinload(AutomationRun.automation),
            selectinload(AutomationRun.trigger),
        )
    )
    result = await session.execute(stmt)
    return result.scalar_one_or_none()


async def current_status(session: AsyncSession, run_id: int) -> RunStatus | None:
    """The run's committed status read from the table (``None`` once deleted)."""
    return (
        await session.execute(select(_runs.c.status).where(_runs.c.id == run_id))
    ).scalar_one_or_none()


async def start_if_pending(session: AsyncSession, run: AutomationRun) -> bool:
    """PENDING → RUNNING, stamping ``started_at``. ``False`` if no longer pending."""
    now = datetime.now(UTC)
    started = (
        await session.execute(
            update(_runs)
            .where(_runs.c.id == run.id, _runs.c.status == RunStatus.PENDING)
            .values(status=RunStatus.RUNNING, started_at=now)
            .returning(_runs.c.id)
        )
    ).scalar_one_or_none()
    if started is None:
        return False
    set_committed_value(run, "status", RunStatus.RUNNING)
    set_committed_value(run, "started_at", now)
    return True


async def finish(
    session: AsyncSession,
    run_id: int,
    status: RunStatus,
    *,
    error: dict[str, Any] | None = None,
    from_statuses: Iterable[RunStatus] = (RunStatus.RUNNING,),
) -> bool:
    """Move the run to terminal ``status`` if it is still in ``from_statuses``.

    Returns whether this call made the change (``False``: someone else already
    finished, cancelled or deleted it).
    """
    finished = (
        await session.execute(
            update(_runs)
            .where(_runs.c.id == run_id, _runs.c.status.in_(list(from_statuses)))
            .values(status=status, finished_at=datetime.now(UTC), error=error)
            .returning(_runs.c.id)
        )
    ).scalar_one_or_none()
    return finished is not None


async def append_step_result(
    session: AsyncSession,
    run: AutomationRun,
    step_result: dict[str, Any],
) -> None:
    """Append one step result. Reassigns the list so SQLAlchemy detects the change."""
    current = list(run.step_results or [])
    current.append(step_result)
    run.step_results = current
    await session.flush()


async def append_step_result_by_id(
    session: AsyncSession, run_id: int, step_result: dict[str, Any]
) -> None:
    """Append a step result in SQL, without the ORM object.

    For the step a time limit or a cancel cut short, written after the
    session was rolled back. Touches only ``step_results``; a deleted run is
    simply not matched.
    """
    entry = bindparam("entry", [step_result], type_=JSONB)
    await session.execute(
        update(_runs)
        .where(_runs.c.id == run_id)
        .values(step_results=_runs.c.step_results.op("||")(entry))
    )
