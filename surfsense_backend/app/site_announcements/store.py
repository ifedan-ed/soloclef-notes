"""The database reads and writes behind sending and withdrawing announcements."""

from __future__ import annotations

from collections.abc import Sequence
from uuid import UUID

from fastapi import Depends
from sqlalchemy import delete, func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import User, Workspace, WorkspaceMembership, get_async_session
from app.notifications.persistence import Notification
from app.notifications.types import SITE_ANNOUNCEMENT_TYPE
from app.site_announcements.persistence import SiteAnnouncement


def _deliveries_of(announcement_id: int):
    return (Notification.type == SITE_ANNOUNCEMENT_TYPE) & (
        Notification.notification_metadata["announcement_id"].astext
        == str(announcement_id)
    )


class AnnouncementStore:
    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def active_user_ids(self) -> list[UUID]:
        rows = await self._session.execute(select(User.id).where(User.is_active))
        return list(rows.scalars().all())

    async def workspace_member_ids(self, workspace_id: int) -> list[UUID] | None:
        """Active members of the workspace, or None when it does not exist."""
        exists = await self._session.scalar(
            select(Workspace.id).where(Workspace.id == workspace_id)
        )
        if exists is None:
            return None
        rows = await self._session.execute(
            select(WorkspaceMembership.user_id)
            .join(User, User.id == WorkspaceMembership.user_id)
            .where(WorkspaceMembership.workspace_id == workspace_id, User.is_active)
        )
        return list(rows.scalars().all())

    async def workspaces(self) -> list[tuple[int, str, int]]:
        """Every workspace with its member count, for choosing an audience."""
        rows = await self._session.execute(
            select(Workspace.id, Workspace.name, func.count(WorkspaceMembership.id))
            .outerjoin(
                WorkspaceMembership, WorkspaceMembership.workspace_id == Workspace.id
            )
            .group_by(Workspace.id, Workspace.name)
            .order_by(Workspace.name)
        )
        return [(row[0], row[1], row[2]) for row in rows.all()]

    async def add_announcement(self, announcement: SiteAnnouncement) -> None:
        self._session.add(announcement)
        await self._session.flush()

    async def add_notifications(self, rows: Sequence[Notification]) -> None:
        self._session.add_all(rows)

    async def get(self, announcement_id: int) -> SiteAnnouncement | None:
        return await self._session.get(SiteAnnouncement, announcement_id)

    async def recent(self, limit: int = 50) -> list[SiteAnnouncement]:
        rows = await self._session.execute(
            select(SiteAnnouncement)
            .order_by(SiteAnnouncement.created_at.desc())
            .limit(limit)
        )
        return list(rows.scalars().all())

    async def read_counts(self, announcement_ids: Sequence[int]) -> dict[int, int]:
        """How many recipients have read each announcement (delivered copies only)."""
        if not announcement_ids:
            return {}
        key = Notification.notification_metadata["announcement_id"].astext
        rows = await self._session.execute(
            select(key, func.count(Notification.id))
            .where(
                Notification.type == SITE_ANNOUNCEMENT_TYPE,
                Notification.read.is_(True),
                key.in_([str(i) for i in announcement_ids]),
            )
            .group_by(key)
        )
        return {int(row[0]): row[1] for row in rows.all()}

    async def workspace_names(self, workspace_ids: Sequence[int]) -> dict[int, str]:
        if not workspace_ids:
            return {}
        rows = await self._session.execute(
            select(Workspace.id, Workspace.name).where(
                Workspace.id.in_(set(workspace_ids))
            )
        )
        return {row[0]: row[1] for row in rows.all()}

    async def delete_deliveries(self, announcement_id: int) -> int:
        result = await self._session.execute(
            delete(Notification).where(_deliveries_of(announcement_id))
        )
        return result.rowcount or 0

    async def commit(self) -> None:
        await self._session.commit()


async def get_announcement_store(
    session: AsyncSession = Depends(get_async_session),
) -> AnnouncementStore:
    return AnnouncementStore(session)
