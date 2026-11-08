"""The per-automation lock that "If already running" decisions are made under.

A Postgres row lock on the ``automations`` row, held until the caller's
transaction ends. Every check-then-act on an automation's runs (may a new run
be created? may this queued run start?) takes it first, so two fires landing
in the same instant -- on two workers, two triggers, or a retried message --
are decided one after the other and never both see "nothing is running".

**Lock order.** A transaction that touches both the automation row and its
run rows takes this lock first: launching a run, starting one
(``start.py``), pausing or archiving (``services/automation.py``) and
cancelling stranded waiting runs (``handoff.py``). Writers of run rows alone
(a person's Cancel, the executor's status writes, the overdue sweep) never ask
for it afterwards. So no two of them can wait on each other in a circle.
"""

from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.persistence.enums.automation_status import AutomationStatus
from app.automations.persistence.models.automation import Automation


async def lock_automation(
    session: AsyncSession, automation_id: int
) -> AutomationStatus | None:
    """Lock the automation row; return its status, or ``None`` if it is gone.

    Blocks while another transaction holds the lock. The caller ends its
    transaction (commit or rollback) to release it.
    """
    stmt = (
        select(Automation.status)
        .where(Automation.id == automation_id)
        .with_for_update()
    )
    return (await session.execute(stmt)).scalar_one_or_none()
