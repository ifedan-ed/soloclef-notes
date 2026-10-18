"""Read a workspace's System Instructions from the database."""

from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Workspace

from .render import normalize_workspace_instructions


async def load_workspace_instructions(
    session: AsyncSession, workspace_id: int
) -> str | None:
    """The workspace's saved instructions, normalized; None when unset."""
    value = await session.scalar(
        select(Workspace.qna_custom_instructions).where(Workspace.id == workspace_id)
    )
    return normalize_workspace_instructions(value)
