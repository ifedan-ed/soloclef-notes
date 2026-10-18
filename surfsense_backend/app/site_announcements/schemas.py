"""Response shapes for the announcements admin API."""

from __future__ import annotations

from datetime import datetime

from pydantic import BaseModel

from app.site_announcements.draft import Audience
from app.site_announcements.expiry import AnnouncementStatus


class AnnouncementOut(BaseModel):
    id: int
    title: str
    body: str
    link: str | None
    audience: Audience
    workspace_id: int | None
    workspace_name: str | None
    created_at: datetime
    expires_at: datetime | None
    withdrawn_at: datetime | None
    recipient_count: int
    read_count: int
    status: AnnouncementStatus


class WorkspaceChoice(BaseModel):
    id: int
    name: str
    member_count: int


class AnnouncementsOverview(BaseModel):
    announcements: list[AnnouncementOut]
    workspaces: list[WorkspaceChoice]
