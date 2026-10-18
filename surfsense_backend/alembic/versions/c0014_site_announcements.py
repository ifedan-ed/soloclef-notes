"""Site announcements: the record of each message an administrator sends.

Delivery reuses ``notifications`` (one row per recipient, type
``site_announcement``, ``metadata.announcement_id`` pointing here), so this
migration adds only the record table. ``notifications`` is unchanged and stays
in the Zero publication with all its columns; ``site_announcements`` is admin
data served over REST and is not published.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0014_site_announcements
Revises: c0013_chunk_source_pages
"""

from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

from alembic import op

revision: str = "c0014_site_announcements"
down_revision: str | None = "c0013_chunk_source_pages"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.create_table(
        "site_announcements",
        sa.Column("id", sa.Integer(), primary_key=True),
        sa.Column("title", sa.String(length=200), nullable=False),
        sa.Column("body", sa.Text(), nullable=False),
        sa.Column("link", sa.String(length=2048), nullable=True),
        sa.Column("audience", sa.String(length=20), nullable=False),
        sa.Column(
            "workspace_id",
            sa.Integer(),
            sa.ForeignKey("workspaces.id", ondelete="CASCADE"),
            nullable=True,
        ),
        sa.Column(
            "created_by_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("user.id", ondelete="SET NULL"),
            nullable=True,
        ),
        sa.Column("expires_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("withdrawn_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("recipient_count", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), nullable=False),
    )
    op.create_index(
        "ix_site_announcements_id", "site_announcements", ["id"], unique=False
    )
    op.create_index(
        "ix_site_announcements_created_at",
        "site_announcements",
        ["created_at"],
        unique=False,
    )


def downgrade() -> None:
    # Delivered copies go with the record they point at.
    op.execute("DELETE FROM notifications WHERE type = 'site_announcement'")
    op.drop_index("ix_site_announcements_created_at", table_name="site_announcements")
    op.drop_index("ix_site_announcements_id", table_name="site_announcements")
    op.drop_table("site_announcements")
