"""The executor's "has this run been ended from outside?" check.

Reads the run's status on a session of its own (the executor's session is busy
with the step), so a Cancel committed by the API, the overdue sweep's Timed
out, or the row's deletion is seen while the step is still working.
"""

from __future__ import annotations

from collections.abc import Callable
from contextlib import AbstractAsyncContextManager

from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.persistence.enums.run_status import RunStatus

from . import repository
from .supervise import StopProbe


def run_ended_elsewhere(
    session_factory: Callable[[], AbstractAsyncContextManager[AsyncSession]],
    run_id: int,
) -> StopProbe:
    """A probe that is ``True`` once run ``run_id`` is no longer Running."""

    async def probe() -> bool:
        async with session_factory() as session:
            status = await repository.current_status(session, run_id)
        return status != RunStatus.RUNNING

    return probe
