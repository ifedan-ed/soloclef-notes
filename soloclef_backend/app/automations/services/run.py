"""``RunService`` — automation run history, and cancelling a run in progress."""

from __future__ import annotations

from fastapi import Depends, HTTPException
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.automations.concurrency.handoff import start_next_waiting_run
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.automation import Automation
from app.automations.persistence.models.run import AutomationRun
from app.automations.runtime.cancellation import UNFINISHED, cancel_unfinished_runs
from app.db import Permission, get_async_session
from app.users import get_auth_context
from app.utils.rbac import check_permission


class RunService:
    """``AutomationRun`` history, plus Cancel for a run that hasn't finished."""

    def __init__(self, *, session: AsyncSession, auth: AuthContext) -> None:
        self.session = session
        self.auth = auth

    async def list(
        self,
        *,
        automation_id: int,
        limit: int,
        offset: int,
    ) -> tuple[list[AutomationRun], int]:
        """Return a page of runs for an automation, newest first."""
        await self._authorize(automation_id, Permission.AUTOMATIONS_READ.value)

        base = select(AutomationRun).where(AutomationRun.automation_id == automation_id)
        total = await self.session.scalar(
            select(func.count()).select_from(base.subquery())
        )

        rows = (
            (
                await self.session.execute(
                    base.order_by(AutomationRun.created_at.desc())
                    .limit(limit)
                    .offset(offset)
                )
            )
            .scalars()
            .all()
        )
        return list(rows), int(total or 0)

    async def get(self, *, automation_id: int, run_id: int) -> AutomationRun:
        await self._authorize(automation_id, Permission.AUTOMATIONS_READ.value)
        run = await self.session.get(AutomationRun, run_id)
        if run is None or run.automation_id != automation_id:
            raise HTTPException(status_code=404, detail=f"run {run_id} not found")
        return run

    async def cancel(self, *, automation_id: int, run_id: int) -> AutomationRun:
        """Cancel a pending or running run (``automations:execute``).

        The run is Cancelled at once; its worker stops within a few seconds
        (``app/automations/runtime/cancellation.py``). The automation's next
        waiting run, if any, is handed to a worker straight away
        (``concurrency/handoff.py``): under "Queue the next run" it would
        otherwise wait for the overdue sweep when the cancelled run was the
        head of the queue, or was running on a worker that is gone. Cancelling
        a run that is already Cancelled returns it unchanged; any other
        finished run is a 409.
        """
        await self._authorize(automation_id, Permission.AUTOMATIONS_EXECUTE.value)
        run = await self.session.get(AutomationRun, run_id)
        if run is None or run.automation_id != automation_id:
            raise HTTPException(status_code=404, detail=f"run {run_id} not found")
        if run.status in UNFINISHED:
            cancelled = await cancel_unfinished_runs(
                self.session, automation_id=automation_id, run_id=run_id
            )
            await self.session.commit()
            if cancelled:
                await start_next_waiting_run(self.session, automation_id)
            await self.session.refresh(run)
        if run.status != RunStatus.CANCELLED:
            raise HTTPException(status_code=409, detail="This run already finished.")
        return run

    async def _authorize(self, automation_id: int, permission: str) -> Automation:
        automation = await self.session.get(Automation, automation_id)
        if automation is None:
            raise HTTPException(
                status_code=404, detail=f"automation {automation_id} not found"
            )
        await check_permission(
            self.session,
            self.auth,
            automation.workspace_id,
            permission,
            f"You don't have permission to {permission.split(':')[1]} automations in this workspace",
        )
        return automation


def get_run_service(
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
) -> RunService:
    return RunService(session=session, auth=auth)
