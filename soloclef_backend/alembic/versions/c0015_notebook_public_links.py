"""Live notebook links: ``notebook_public_links``, at most one per notebook.

A live link shows a notebook as it is at each request (its chat, finished
Studio outputs and sources), unlike ``public_chat_snapshots``, which freeze a
copy. Deleting the notebook or its workspace deletes the link; a deleted
creator leaves ``created_by_user_id`` null, which the public routes treat as
revoked. Admin data served over REST: not added to the Zero publication.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0015_notebook_public_links
Revises: c0014_site_announcements
"""

from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

from alembic import op

revision: str = "c0015_notebook_public_links"
down_revision: str | None = "c0014_site_announcements"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.create_table(
        "notebook_public_links",
        sa.Column("id", sa.Integer(), primary_key=True),
        sa.Column(
            "notebook_id",
            sa.Integer(),
            sa.ForeignKey("notebooks.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column(
            "workspace_id",
            sa.Integer(),
            sa.ForeignKey("workspaces.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("share_token", sa.String(length=128), nullable=False),
        sa.Column(
            "created_by_user_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("user.id", ondelete="SET NULL"),
            nullable=True,
        ),
        sa.Column("expires_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), nullable=False),
        sa.UniqueConstraint("notebook_id", name="uq_notebook_public_links_notebook"),
    )
    op.create_index(
        "ix_notebook_public_links_id", "notebook_public_links", ["id"], unique=False
    )
    op.create_index(
        "ix_notebook_public_links_share_token",
        "notebook_public_links",
        ["share_token"],
        unique=True,
    )
    op.create_index(
        "ix_notebook_public_links_workspace_id",
        "notebook_public_links",
        ["workspace_id"],
        unique=False,
    )
    op.create_index(
        "ix_notebook_public_links_created_at",
        "notebook_public_links",
        ["created_at"],
        unique=False,
    )


def downgrade() -> None:
    op.drop_table("notebook_public_links")
