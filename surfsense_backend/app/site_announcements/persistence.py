"""The record of each announcement an administrator sent."""

from __future__ import annotations

from sqlalchemy import TIMESTAMP, Column, ForeignKey, Integer, String, Text
from sqlalchemy.dialects.postgresql import UUID

from app.db import BaseModel, TimestampMixin


class SiteAnnouncement(BaseModel, TimestampMixin):
    __tablename__ = "site_announcements"

    title = Column(String(200), nullable=False)
    body = Column(Text, nullable=False)
    link = Column(String(2048), nullable=True)
    # "all" (every active account) or "workspace" (that workspace's members).
    audience = Column(String(20), nullable=False)
    workspace_id = Column(
        Integer, ForeignKey("workspaces.id", ondelete="CASCADE"), nullable=True
    )
    created_by_id = Column(
        UUID(as_uuid=True), ForeignKey("user.id", ondelete="SET NULL"), nullable=True
    )
    expires_at = Column(TIMESTAMP(timezone=True), nullable=True)
    withdrawn_at = Column(TIMESTAMP(timezone=True), nullable=True)
    recipient_count = Column(Integer, nullable=False, default=0, server_default="0")
    # Recipients who read their copy and then deleted it from their inbox: still
    # read, so the Sent list's "read by" does not drop (app/site_announcements/cleared_reads.py).
    cleared_read_count = Column(Integer, nullable=False, default=0, server_default="0")
