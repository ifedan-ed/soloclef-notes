"""HTTP routes for deleting from the inbox: one notification, or a whole view.

Rows are deleted outright; Zero replicates the delete, so every open tab and
device drops them. Only the caller's own rows are ever touched: an
administrator's announcement stays in every other recipient's inbox.

A running job's notification (metadata status "in_progress") is never
deleted: the job still holds the row and updates it as it goes, and an UPDATE
of a deleted row fails the job (an upload indexed fine would be marked
failed, a Drive sync would never record its time). Clear all leaves it in the
inbox and deleting it alone is a 409. It can go once the job has finished, or
once cleanup_stale_indexing_notifications (every 5 minutes) has marked a job
that died as failed.
"""

from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter, Depends, HTTPException, Query, status
from sqlalchemy import delete, func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import get_async_session
from app.notifications.api.schemas import (
    ClearNotificationsResponse,
    DeleteNotificationResponse,
)
from app.notifications.api.scope import InboxPreset, inbox_view
from app.notifications.api.transform import parse_source_type
from app.notifications.persistence import Notification
from app.notifications.types import NotificationCategory, NotificationType
from app.site_announcements.cleared_reads import DeletedCopy, keep_reads_of
from app.users import require_session_context

router = APIRouter(prefix="/notifications", tags=["notifications"])

_DELETED_COPY = (
    Notification.type,
    Notification.read,
    Notification.notification_metadata["announcement_id"].astext,
)


# A job still running holds its notification: never deleted (see the docstring).
# coalesce: a row with no "status" key is not running, and NULL would skip it.
JOB_RUNNING = (
    func.coalesce(Notification.notification_metadata["status"].astext, "")
    == "in_progress"
)


async def _delete_where(session: AsyncSession, *conditions) -> int:
    result = await session.execute(
        delete(Notification)
        .where(*conditions, ~JOB_RUNNING)
        .returning(*_DELETED_COPY)
        .execution_options(synchronize_session=False)
    )
    deleted = [DeletedCopy(*row) for row in result.all()]
    await keep_reads_of(session, deleted)
    await session.commit()
    return len(deleted)


@router.delete("/{notification_id}", response_model=DeleteNotificationResponse)
async def delete_notification(
    notification_id: int,
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
) -> DeleteNotificationResponse:
    """Delete one of the caller's notifications; another account's is a 404,
    a running job's a 409."""
    mine = (
        Notification.id == notification_id,
        Notification.user_id == auth.user.id,
    )
    deleted = await _delete_where(session, *mine)
    if not deleted:
        if await session.scalar(select(Notification.id).where(*mine)) is not None:
            raise HTTPException(
                status_code=status.HTTP_409_CONFLICT,
                detail="Still running. It can be deleted once it has finished.",
            )
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail="Notification not found",
        )
    return DeleteNotificationResponse(success=True, message="Notification deleted")


@router.delete("", response_model=ClearNotificationsResponse)
async def clear_notifications(
    workspace_id: int | None = Query(None, description="Filter by workspace ID"),
    type_filter: NotificationType | None = Query(
        None, alias="type", description="Filter by notification type"
    ),
    category: NotificationCategory | None = Query(
        None, description="Filter by category: 'comments', 'status' or 'announcements'"
    ),
    source_type: str | None = Query(
        None,
        description="Filter by source type, e.g. 'connector:GOOGLE_DRIVE_CONNECTOR' or 'doctype:FILE'",
    ),
    filter: InboxPreset | None = Query(
        None,
        description="Filter preset: 'unread' for unread only, 'errors' for failed/error items only",
    ),
    search: str | None = Query(
        None, description="Search notifications by title or message (case-insensitive)"
    ),
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
) -> ClearNotificationsResponse:
    """Delete every notification the list shows for the same filters, but a
    running job's.

    No filters clears the caller's whole inbox. Unlike the list, an unknown
    filter is refused rather than ignored, so a typo never widens the view.
    """
    source = parse_source_type(source_type) if source_type else None
    if source_type and source is None:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail="Unknown source type. Use 'connector:<type>' or 'doctype:<type>'.",
        )
    deleted = await _delete_where(
        session,
        *inbox_view(
            auth.user.id,
            datetime.now(UTC),
            workspace_id=workspace_id,
            type_filter=type_filter,
            category=category,
            source=source,
            preset=filter,
            search=search,
        ),
    )
    return ClearNotificationsResponse(
        success=True,
        message=f"Deleted {deleted} notification(s)",
        deleted_count=deleted,
    )
