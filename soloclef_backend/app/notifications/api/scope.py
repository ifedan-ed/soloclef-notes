"""Which notifications an inbox view covers.

The list and clearing both build their WHERE clause here, so "Clear all" on a
tab removes exactly the rows that tab lists, for the caller only.
"""

from __future__ import annotations

from datetime import datetime
from typing import Literal
from uuid import UUID

from sqlalchemy.sql.elements import ColumnElement

from app.notifications.api.transform import SourceTypeFilter
from app.notifications.constants import CATEGORY_TYPES
from app.notifications.persistence import Notification
from app.site_announcements.expiry import unexpired

InboxPreset = Literal["unread", "errors"]


def inbox_view(
    user_id: UUID,
    now: datetime,
    *,
    workspace_id: int | None = None,
    type_filter: str | None = None,
    category: str | None = None,
    source: SourceTypeFilter | None = None,
    preset: str | None = None,
    search: str | None = None,
) -> list[ColumnElement[bool]]:
    conditions: list[ColumnElement[bool]] = [
        Notification.user_id == user_id,
        unexpired(now),
    ]

    if workspace_id is not None:
        # Notifications with no workspace show in every workspace.
        conditions.append(
            (Notification.workspace_id == workspace_id)
            | (Notification.workspace_id.is_(None))
        )

    if type_filter:
        conditions.append(Notification.type == type_filter)

    if category:
        conditions.append(Notification.type.in_(CATEGORY_TYPES[category]))

    if source is not None:
        conditions.append(
            Notification.type.in_(source.types)
            & (
                Notification.notification_metadata[source.metadata_key].astext
                == source.value
            )
        )

    if preset == "unread":
        conditions.append(Notification.read == False)  # noqa: E712
    elif preset == "errors":
        conditions.append(
            (Notification.type == "insufficient_credits")
            | (Notification.notification_metadata["status"].astext == "failed")
        )

    if search:
        term = f"%{search}%"
        conditions.append(
            Notification.title.ilike(term) | Notification.message.ilike(term)
        )

    return conditions
