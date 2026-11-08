"""Cancel runs that have not finished: a person's Cancel, or pausing the automation.

Cancelling is a status write, not a signal: the run row moves to Cancelled at
once, and the worker executing it notices on its next status check (every
``STOP_POLL_SECONDS``, see ``supervise.py``) and stops the step it is on.
A run still waiting to start never starts. No result email is sent for a
cancelled run -- the person who cancelled it already knows.

A run left waiting to start for too long is Cancelled too, by the queue
hand-off rather than a person, with :func:`not_started_error`
(``app/automations/concurrency/handoff.py``).

Deleting an automation deletes its runs with it (foreign-key cascade); a
worker mid-run sees its row gone on the same check and stops, so there is no
Cancelled row left to show.
"""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Any

from sqlalchemy import update
from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.persistence.enums.automation_status import AutomationStatus
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.run import AutomationRun

UNFINISHED = (RunStatus.PENDING, RunStatus.RUNNING)

_runs = AutomationRun.__table__


def cancelled_error(cause: AutomationStatus | None = None) -> dict[str, Any]:
    """The run-level ``error`` a cancelled run carries, worded for the run page.

    ``cause`` is the automation's new status when pausing or archiving it
    cancelled the run; ``None`` means a person cancelled this run.
    """
    if cause == AutomationStatus.PAUSED:
        message = "Cancelled because the automation was paused."
    elif cause == AutomationStatus.ARCHIVED:
        message = "Cancelled because the automation was archived."
    else:
        message = "Cancelled before it finished."
    return {"type": "Cancelled", "message": message}


def not_started_error(waited: str) -> dict[str, Any]:
    """The ``error`` of a run dropped because it waited too long to start.

    ``waited`` is the start window in words ("1 hour"). Typed ``Cancelled``
    so the run page shows it like any cancelled run, with this reason.
    """
    return {
        "type": "Cancelled",
        "message": (
            f"Not started: it was still waiting to start {waited} after it "
            "could have, so it was dropped rather than run late."
        ),
    }


async def cancel_unfinished_runs(
    session: AsyncSession,
    *,
    automation_id: int,
    run_id: int | None = None,
    cause: AutomationStatus | None = None,
) -> list[int]:
    """Mark the automation's pending/running runs (or just ``run_id``) Cancelled.

    Returns the ids actually cancelled; runs that already finished are left
    as they are. The caller commits.
    """
    stmt = (
        update(_runs)
        .where(
            _runs.c.automation_id == automation_id,
            _runs.c.status.in_(UNFINISHED),
        )
        .values(
            status=RunStatus.CANCELLED,
            finished_at=datetime.now(UTC),
            error=cancelled_error(cause),
        )
        .returning(_runs.c.id)
    )
    if run_id is not None:
        stmt = stmt.where(_runs.c.id == run_id)
    return list((await session.execute(stmt)).scalars().all())
