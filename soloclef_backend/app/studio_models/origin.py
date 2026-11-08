"""Whether a queued run is a Studio output, and the plan it runs on.

Only runs started from a notebook's Studio panel have a ``NotebookOutput``;
the same queues also serve chat-started videos and podcasts, which keep the
chat model.
"""

from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Workspace
from app.notebooks.persistence import NotebookOutput

from .resolution import StudioPlan, resolve_plan
from .slots import slot_for


async def _plan_for(session: AsyncSession, condition) -> StudioPlan | None:
    row = (
        await session.execute(
            select(
                NotebookOutput.kind, NotebookOutput.options, NotebookOutput.workspace_id
            ).where(condition)
        )
    ).first()
    if row is None:
        return None
    slot = slot_for(row.kind, row.options)
    workspace = await session.get(Workspace, row.workspace_id)
    if slot is None or workspace is None:
        return None
    return await resolve_plan(session, workspace, slot)


async def plan_for_job(session: AsyncSession, job_id: int) -> StudioPlan | None:
    return await _plan_for(session, NotebookOutput.deliverable_job_id == job_id)


async def plan_for_podcast(session: AsyncSession, podcast_id: int) -> StudioPlan | None:
    return await _plan_for(session, NotebookOutput.podcast_id == podcast_id)
