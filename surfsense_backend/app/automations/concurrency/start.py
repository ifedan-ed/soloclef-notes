"""Claim a pending run for this worker, or tell it to wait its turn.

Called by the executor before any step runs. Under :func:`lock_automation`:

* a run that is no longer pending (already started by a duplicate message,
  cancelled, deleted) is left alone;
* a run whose automation is no longer active (paused or archived after the
  fire) is marked Cancelled instead of started;
* under ``queue`` ("Queue the next run"), a run waits while another run of
  the automation is running, or an older one is still waiting -- runs start
  one at a time, oldest first. A waiting run stays Pending; the run ahead of
  it hands it to a worker when it ends (``handoff.py``);
* otherwise the run moves to Running and this worker owns it.
"""

from __future__ import annotations

from enum import StrEnum

from sqlalchemy import exists, or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.persistence.enums.automation_status import AutomationStatus
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.run import AutomationRun
from app.automations.runtime import repository
from app.automations.runtime.cancellation import cancelled_error

from .lock import lock_automation


class StartClaim(StrEnum):
    STARTED = "started"
    WAIT = "wait"
    GONE = "gone"
    CANCELLED = "cancelled"


async def claim_start(
    session: AsyncSession, run: AutomationRun, policy: str
) -> StartClaim:
    """Decide and record whether ``run`` starts now. Commits either way."""
    automation_status = await lock_automation(session, run.automation_id)
    if automation_status is None:
        await session.rollback()
        return StartClaim.GONE

    if await repository.current_status(session, run.id) != RunStatus.PENDING:
        await session.commit()
        return StartClaim.GONE

    if automation_status != AutomationStatus.ACTIVE:
        cancelled = await repository.finish(
            session,
            run.id,
            RunStatus.CANCELLED,
            error=cancelled_error(automation_status),
            from_statuses=(RunStatus.PENDING,),
        )
        await session.commit()
        return StartClaim.CANCELLED if cancelled else StartClaim.GONE

    if policy == "queue" and await _has_run_ahead(session, run):
        await session.commit()
        return StartClaim.WAIT

    started = await repository.start_if_pending(session, run)
    await session.commit()
    return StartClaim.STARTED if started else StartClaim.GONE


async def _has_run_ahead(session: AsyncSession, run: AutomationRun) -> bool:
    """Another run of the automation is running, or has waited longer."""
    other = AutomationRun
    stmt = select(
        exists().where(
            other.automation_id == run.automation_id,
            other.id != run.id,
            or_(
                other.status == RunStatus.RUNNING,
                (other.status == RunStatus.PENDING) & (other.id < run.id),
            ),
        )
    )
    return bool((await session.execute(stmt)).scalar())
