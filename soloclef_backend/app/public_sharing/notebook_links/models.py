"""``notebook_public_links``: at most one live link per notebook."""

from __future__ import annotations

from sqlalchemy import TIMESTAMP, Column, ForeignKey, Integer, String, UniqueConstraint
from sqlalchemy.dialects.postgresql import UUID

from app.db import BaseModel, TimestampMixin


class NotebookPublicLink(BaseModel, TimestampMixin):
    __tablename__ = "notebook_public_links"
    __table_args__ = (
        UniqueConstraint("notebook_id", name="uq_notebook_public_links_notebook"),
    )

    # Deleting the notebook (or its workspace) deletes the link with it.
    notebook_id = Column(
        Integer,
        ForeignKey("notebooks.id", ondelete="CASCADE"),
        nullable=False,
    )
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    share_token = Column(String(128), nullable=False, unique=True, index=True)
    created_by_user_id = Column(
        UUID(as_uuid=True), ForeignKey("user.id", ondelete="SET NULL"), nullable=True
    )
    expires_at = Column(TIMESTAMP(timezone=True), nullable=True)
