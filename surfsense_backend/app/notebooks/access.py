"""Authorize a caller against a workspace and load a notebook inside it.

Every notebook route goes through here, so the workspace boundary is enforced
in one place: permission is checked on the workspace named in the URL, and the
notebook is only found if it also belongs to that workspace. A notebook id from
another workspace is indistinguishable from a missing one (404).
"""

from __future__ import annotations

from datetime import UTC, datetime

from fastapi import HTTPException
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission
from app.utils.rbac import check_permission

from .persistence import Notebook

_DENIED = {
    Permission.CHATS_READ: "You don't have permission to view notebooks in this workspace",
    Permission.CHATS_CREATE: "You don't have permission to create notebooks in this workspace",
    Permission.CHATS_UPDATE: "You don't have permission to edit notebooks in this workspace",
    Permission.CHATS_DELETE: "You don't have permission to delete notebooks in this workspace",
}


async def authorize(
    session: AsyncSession,
    auth: AuthContext,
    workspace_id: int,
    *permissions: Permission,
) -> None:
    for permission in permissions:
        await check_permission(
            session,
            auth,
            workspace_id,
            permission.value,
            _DENIED.get(
                permission,
                "You don't have permission to perform this action",
            ),
        )


async def load_notebook(
    session: AsyncSession, workspace_id: int, notebook_id: int
) -> Notebook:
    notebook = await session.scalar(
        select(Notebook).where(
            Notebook.id == notebook_id,
            Notebook.workspace_id == workspace_id,
        )
    )
    if notebook is None:
        raise HTTPException(status_code=404, detail="Notebook not found")
    return notebook


async def authorized_notebook(
    session: AsyncSession,
    auth: AuthContext,
    workspace_id: int,
    notebook_id: int,
    *permissions: Permission,
) -> Notebook:
    await authorize(session, auth, workspace_id, *permissions)
    return await load_notebook(session, workspace_id, notebook_id)


def touch(notebook: Notebook) -> None:
    """Mark activity so the notebook rises in the recent list."""
    notebook.updated_at = datetime.now(UTC)
