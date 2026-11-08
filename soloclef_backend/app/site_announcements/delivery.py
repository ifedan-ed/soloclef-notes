"""One inbox notification per recipient for an announcement."""

from __future__ import annotations

from collections.abc import Iterable
from uuid import UUID

from app.notifications.persistence import Notification
from app.notifications.types import SITE_ANNOUNCEMENT_TYPE
from app.site_announcements.persistence import SiteAnnouncement


def delivery_metadata(announcement: SiteAnnouncement) -> dict:
    """What the inbox needs besides title and body: the link, the expiry, the source."""
    return {
        "announcement_id": announcement.id,
        "link": announcement.link,
        "expires_at": announcement.expires_at.isoformat()
        if announcement.expires_at
        else None,
    }


def notification_rows(
    announcement: SiteAnnouncement, user_ids: Iterable[UUID]
) -> list[Notification]:
    """Unread copies for each recipient, each with its own read state.

    A workspace announcement is filed under that workspace; a site-wide one has
    no workspace, so the inbox shows it in every workspace.
    """
    metadata = delivery_metadata(announcement)
    return [
        Notification(
            user_id=user_id,
            workspace_id=announcement.workspace_id,
            type=SITE_ANNOUNCEMENT_TYPE,
            title=announcement.title,
            message=announcement.body,
            read=False,
            notification_metadata=dict(metadata),
            created_at=announcement.created_at,
            updated_at=announcement.created_at,
        )
        for user_id in dict.fromkeys(user_ids)
    ]
