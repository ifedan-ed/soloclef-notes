"""An in-memory stand-in for the announcements store."""

from __future__ import annotations

from uuid import UUID

from app.notifications.persistence import Notification
from app.site_announcements.persistence import SiteAnnouncement


class FakeStore:
    def __init__(
        self,
        users: list[UUID] | None = None,
        members: dict[int, list[UUID]] | None = None,
    ) -> None:
        self.users = users or []
        self.members = members or {}
        self.announcements: dict[int, SiteAnnouncement] = {}
        self.inbox: list[Notification] = []
        self.commits = 0

    async def active_user_ids(self) -> list[UUID]:
        return list(self.users)

    async def workspace_member_ids(self, workspace_id: int) -> list[UUID] | None:
        return self.members.get(workspace_id)

    async def workspaces(self) -> list[tuple[int, str, int]]:
        return [(ws, f"Workspace {ws}", len(m)) for ws, m in self.members.items()]

    async def add_announcement(self, announcement: SiteAnnouncement) -> None:
        announcement.id = len(self.announcements) + 1
        self.announcements[announcement.id] = announcement

    async def add_notifications(self, rows: list[Notification]) -> None:
        self.inbox.extend(rows)

    async def get(self, announcement_id: int) -> SiteAnnouncement | None:
        return self.announcements.get(announcement_id)

    async def recent(self, limit: int = 50) -> list[SiteAnnouncement]:
        return list(reversed(self.announcements.values()))[:limit]

    async def read_counts(self, announcement_ids) -> dict[int, int]:
        counts: dict[int, int] = {}
        for row in self.inbox:
            key = row.notification_metadata["announcement_id"]
            if row.read and key in announcement_ids:
                counts[key] = counts.get(key, 0) + 1
        return counts

    async def workspace_names(self, workspace_ids) -> dict[int, str]:
        return {ws: f"Workspace {ws}" for ws in workspace_ids}

    async def delete_deliveries(self, announcement_id: int) -> int:
        before = len(self.inbox)
        self.inbox = [
            row
            for row in self.inbox
            if row.notification_metadata["announcement_id"] != announcement_id
        ]
        return before - len(self.inbox)

    async def commit(self) -> None:
        self.commits += 1

    def inbox_of(self, user_id: UUID) -> list[Notification]:
        return [row for row in self.inbox if row.user_id == user_id]
