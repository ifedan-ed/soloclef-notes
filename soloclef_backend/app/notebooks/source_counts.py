"""Per-notebook source totals for list and detail views."""

from __future__ import annotations

from sqlalchemy import Integer, cast, func, select
from sqlalchemy.ext.asyncio import AsyncSession

from .persistence import NotebookSource


async def source_counts(
    session: AsyncSession, notebook_ids: list[int]
) -> dict[int, tuple[int, int]]:
    """``{notebook_id: (all sources, enabled sources)}``."""
    if not notebook_ids:
        return {}
    rows = await session.execute(
        select(
            NotebookSource.notebook_id,
            func.count(NotebookSource.id),
            func.coalesce(func.sum(cast(NotebookSource.enabled, Integer)), 0),
        )
        .where(NotebookSource.notebook_id.in_(notebook_ids))
        .group_by(NotebookSource.notebook_id)
    )
    return {row[0]: (int(row[1]), int(row[2])) for row in rows.all()}
