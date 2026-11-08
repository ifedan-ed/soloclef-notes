"""The stored choice: one row per workspace that has changed the recommended setup."""

from __future__ import annotations

from datetime import UTC, datetime

from sqlalchemy import Column, ForeignKey, Integer
from sqlalchemy.dialects.postgresql import JSONB, TIMESTAMP

from app.db import Base


def _now() -> datetime:
    return datetime.now(UTC)


class WorkspaceStudioModel(Base):
    """No row means the recommended setup (see ``recommended``).

    ``default_choice`` and each value of ``overrides`` are a ``StudioChoice``
    as JSON; an output type missing from ``overrides`` uses the default.
    """

    __tablename__ = "workspace_studio_models"

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        primary_key=True,
    )
    default_choice = Column(JSONB, nullable=False)
    overrides = Column(JSONB, nullable=False, default=dict)
    updated_at = Column(
        TIMESTAMP(timezone=True), nullable=False, default=_now, onupdate=_now
    )
