"""Whether a trigger that just fired may create a run ("If already running").

Decided when the fire happens, under :func:`lock_automation`:

* ``drop_if_running`` ("Skip if already running", the default): no new run
  while another run of the automation is waiting to start or running. The
  fire is dropped and only logged; nothing appears in the run list.
* ``queue`` ("Queue the next run"): the new run is always created and waits
  its turn (see ``start.py``), up to ``QUEUE_LIMIT`` waiting runs; a fire
  beyond that is dropped and logged, so a burst of events cannot pile up an
  unbounded backlog of paid runs.
* ``always`` ("Always run"): every fire creates a run that starts at once,
  side by side with any run already going.
"""

from __future__ import annotations

from enum import StrEnum

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.run import AutomationRun

# Most runs of one automation that may wait (pending) under "Queue the next run".
QUEUE_LIMIT = 20


class Admission(StrEnum):
    CREATE = "create"
    SKIP_ALREADY_RUNNING = "skip_already_running"
    SKIP_QUEUE_FULL = "skip_queue_full"


def decide_admission(policy: str, *, running: int, waiting: int) -> Admission:
    """Pure rule behind :func:`admit_new_run` (counts exclude the new fire)."""
    if policy == "drop_if_running":
        if running or waiting:
            return Admission.SKIP_ALREADY_RUNNING
        return Admission.CREATE
    if policy == "queue":
        if waiting >= QUEUE_LIMIT:
            return Admission.SKIP_QUEUE_FULL
        return Admission.CREATE
    return Admission.CREATE


async def admit_new_run(
    session: AsyncSession, automation_id: int, policy: str
) -> Admission:
    """Count the automation's unfinished runs and apply the policy.

    The caller must already hold :func:`lock_automation` for ``automation_id``
    and create the run (if any) in the same transaction.
    """
    if policy == "always":
        return Admission.CREATE
    running, waiting = (
        await session.execute(
            select(
                func.count().filter(AutomationRun.status == RunStatus.RUNNING),
                func.count().filter(AutomationRun.status == RunStatus.PENDING),
            ).where(AutomationRun.automation_id == automation_id)
        )
    ).one()
    return decide_admission(policy, running=int(running), waiting=int(waiting))
