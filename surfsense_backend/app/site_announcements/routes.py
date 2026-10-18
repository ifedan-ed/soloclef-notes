"""Admin routes: list sent announcements, send one, withdraw one."""

from __future__ import annotations

from datetime import UTC, datetime

from fastapi import APIRouter, Depends, HTTPException, status

from app.auth.context import AuthContext
from app.site_announcements.admin_access import require_site_admin
from app.site_announcements.draft import AnnouncementDraft
from app.site_announcements.expiry import announcement_status
from app.site_announcements.persistence import SiteAnnouncement
from app.site_announcements.schemas import (
    AnnouncementOut,
    AnnouncementsOverview,
    WorkspaceChoice,
)
from app.site_announcements.sending import (
    UnknownAnnouncementError,
    UnknownWorkspaceError,
    send_announcement,
    withdraw_announcement,
)
from app.site_announcements.store import AnnouncementStore, get_announcement_store

router = APIRouter(prefix="/admin/announcements", tags=["site-announcements"])


def _out(
    announcement: SiteAnnouncement,
    *,
    now: datetime,
    read_count: int = 0,
    workspace_name: str | None = None,
) -> AnnouncementOut:
    return AnnouncementOut(
        id=announcement.id,
        title=announcement.title,
        body=announcement.body,
        link=announcement.link,
        audience=announcement.audience,
        workspace_id=announcement.workspace_id,
        workspace_name=workspace_name,
        created_at=announcement.created_at,
        expires_at=announcement.expires_at,
        withdrawn_at=announcement.withdrawn_at,
        recipient_count=announcement.recipient_count or 0,
        read_count=read_count,
        status=announcement_status(announcement, now),
    )


@router.get("", response_model=AnnouncementsOverview)
async def list_announcements(
    _: AuthContext = Depends(require_site_admin),
    store: AnnouncementStore = Depends(get_announcement_store),
) -> AnnouncementsOverview:
    now = datetime.now(UTC)
    sent = await store.recent()
    reads = await store.read_counts([a.id for a in sent])
    names = await store.workspace_names(
        [a.workspace_id for a in sent if a.workspace_id]
    )
    workspaces = await store.workspaces()
    return AnnouncementsOverview(
        announcements=[
            _out(
                a,
                now=now,
                # Read copies still there, plus read copies their recipient deleted.
                read_count=reads.get(a.id, 0) + (a.cleared_read_count or 0),
                workspace_name=names.get(a.workspace_id) if a.workspace_id else None,
            )
            for a in sent
        ],
        workspaces=[
            WorkspaceChoice(id=ws_id, name=name, member_count=count)
            for ws_id, name, count in workspaces
        ],
    )


@router.post("", response_model=AnnouncementOut, status_code=status.HTTP_201_CREATED)
async def send(
    draft: AnnouncementDraft,
    auth: AuthContext = Depends(require_site_admin),
    store: AnnouncementStore = Depends(get_announcement_store),
) -> AnnouncementOut:
    now = datetime.now(UTC)
    try:
        announcement = await send_announcement(store, draft, auth.user.id, now)
    except UnknownWorkspaceError:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Workspace not found."
        ) from None
    return _out(announcement, now=now)


@router.post("/{announcement_id}/withdraw", response_model=AnnouncementOut)
async def withdraw(
    announcement_id: int,
    _: AuthContext = Depends(require_site_admin),
    store: AnnouncementStore = Depends(get_announcement_store),
) -> AnnouncementOut:
    now = datetime.now(UTC)
    try:
        announcement = await withdraw_announcement(store, announcement_id, now)
    except UnknownAnnouncementError:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Announcement not found."
        ) from None
    return _out(announcement, now=now)
