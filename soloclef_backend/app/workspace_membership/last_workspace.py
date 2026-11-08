"""Nobody deletes or leaves their only workspace.

A default workspace is created only at registration (``app.users``); after
that nothing recreates one, so a person left with none would land on an empty
dashboard. Deleting or leaving the last workspace is refused instead, with a
message that says what to do.
"""

from __future__ import annotations

import uuid

from fastapi import HTTPException
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Workspace, WorkspaceMembership

LAST_WORKSPACE_DELETE_DETAIL = (
    "This is your only workspace, so it can't be deleted. Create another "
    "workspace first, then delete this one."
)
LAST_WORKSPACE_LEAVE_DETAIL = (
    "This is your only workspace, so you can't leave it. Create or join "
    "another workspace first."
)


async def other_workspace_count(
    session: AsyncSession, user_id: uuid.UUID, workspace_id: int
) -> int:
    """Workspaces the user belongs to besides ``workspace_id``, not being deleted."""
    return (
        await session.execute(
            select(func.count())
            .select_from(WorkspaceMembership)
            .join(Workspace, Workspace.id == WorkspaceMembership.workspace_id)
            .where(
                WorkspaceMembership.user_id == user_id,
                WorkspaceMembership.workspace_id != workspace_id,
                ~Workspace.name.startswith("[DELETING] "),
            )
        )
    ).scalar_one()


async def refuse_if_last_workspace(
    session: AsyncSession, user_id: uuid.UUID, workspace_id: int, *, detail: str
) -> None:
    if await other_workspace_count(session, user_id, workspace_id) == 0:
        raise HTTPException(status_code=409, detail=detail)
