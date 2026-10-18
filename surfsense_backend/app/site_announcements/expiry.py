"""When an announcement stops showing: withdrawn, or past its expiry."""

from __future__ import annotations

from datetime import datetime
from typing import Literal

from sqlalchemy import TIMESTAMP, cast, or_
from sqlalchemy.sql.elements import ColumnElement

from app.notifications.persistence import Notification
from app.notifications.types import SITE_ANNOUNCEMENT_TYPE
from app.site_announcements.persistence import SiteAnnouncement

AnnouncementStatus = Literal["live", "expired", "withdrawn"]


def announcement_status(
    announcement: SiteAnnouncement, now: datetime
) -> AnnouncementStatus:
    if announcement.withdrawn_at is not None:
        return "withdrawn"
    if announcement.expires_at is not None and announcement.expires_at <= now:
        return "expired"
    return "live"


def unexpired(now: datetime) -> ColumnElement[bool]:
    """Inbox filter: every notification except an announcement past its expiry."""
    expires = Notification.notification_metadata["expires_at"].astext
    return or_(
        Notification.type != SITE_ANNOUNCEMENT_TYPE,
        expires.is_(None),
        cast(expires, TIMESTAMP(timezone=True)) > now,
    )
