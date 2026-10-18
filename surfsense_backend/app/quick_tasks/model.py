"""The stored choice: one row per workspace that has changed the default."""

from __future__ import annotations

from datetime import UTC, datetime

from sqlalchemy import Column, ForeignKey, Integer
from sqlalchemy.dialects.postgresql import TIMESTAMP

from app.db import Base


def _now() -> datetime:
    return datetime.now(UTC)


class WorkspaceQuickModel(Base):
    """No row means the default model; ``model_id`` 0 means "same as chat"."""

    __tablename__ = "workspace_quick_models"

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        primary_key=True,
    )
    model_id = Column(Integer, nullable=False)
    updated_at = Column(
        TIMESTAMP(timezone=True), nullable=False, default=_now, onupdate=_now
    )
