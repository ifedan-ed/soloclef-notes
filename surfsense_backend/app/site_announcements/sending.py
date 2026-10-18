"""Sending an announcement to its recipients, and withdrawing it from them."""

from __future__ import annotations

from datetime import datetime
from typing import Protocol
from uuid import UUID

from app.notifications.persistence import Notification
from app.push import events as push_events
from app.site_announcements.delivery import notification_rows
from app.site_announcements.draft import AnnouncementDraft
from app.site_announcements.persistence import SiteAnnouncement


class UnknownWorkspaceError(LookupError):
    """The draft names a workspace that does not exist."""


class UnknownAnnouncementError(LookupError):
    """No announcement has that id."""


class Store(Protocol):
    async def active_user_ids(self) -> list[UUID]: ...
    async def workspace_member_ids(self, workspace_id: int) -> list[UUID] | None: ...
    async def add_announcement(self, announcement: SiteAnnouncement) -> None: ...
    async def add_notifications(self, rows: list[Notification]) -> None: ...
    async def get(self, announcement_id: int) -> SiteAnnouncement | None: ...
    async def delete_deliveries(self, announcement_id: int) -> int: ...
    async def commit(self) -> None: ...


async def send_announcement(
    store: Store, draft: AnnouncementDraft, sender_id: UUID, now: datetime
) -> SiteAnnouncement:
    if draft.audience == "workspace":
        recipients = await store.workspace_member_ids(draft.workspace_id)
        if recipients is None:
            raise UnknownWorkspaceError(draft.workspace_id)
    else:
        recipients = await store.active_user_ids()

    announcement = SiteAnnouncement(
        title=draft.title,
        body=draft.body,
        link=draft.link,
        audience=draft.audience,
        workspace_id=draft.workspace_id,
        created_by_id=sender_id,
        expires_at=draft.expires_at,
        created_at=now,
    )
    await store.add_announcement(announcement)
    rows = notification_rows(announcement, recipients)
    announcement.recipient_count = len(rows)
    await store.add_notifications(rows)
    await store.commit()
    # The phone: "New announcement" to each recipient (app/push).
    push_events.announced([row.user_id for row in rows], title=announcement.title)
    return announcement


async def withdraw_announcement(
    store: Store, announcement_id: int, now: datetime
) -> SiteAnnouncement:
    """Take it out of every inbox; the record stays, marked withdrawn."""
    announcement = await store.get(announcement_id)
    if announcement is None:
        raise UnknownAnnouncementError(announcement_id)
    if announcement.withdrawn_at is None:
        announcement.withdrawn_at = now
    await store.delete_deliveries(announcement_id)
    await store.commit()
    return announcement
