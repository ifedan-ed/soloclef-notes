"""Push notifications for the phone app: devices, choices, tickets.

``push_devices`` holds one row per phone (an Expo push token, unique: a
phone that signs into another account moves with it). ``push_preferences``
holds a person's per-kind choices as JSON (a missing row means every kind
on). ``push_tickets`` keeps the ticket Expo answers for each sent message
until its receipt has been read, about 15 minutes later, so the token of a
phone that uninstalled the app can be dropped (``app/push``,
``custom/docs/push-notifications.md``).

None of these is in the Zero publication: the app reads them over REST.

Local revision, namespaced as custom/README.md explains. Written as c0024
beside the account track's c0024_app_sessions and re-chained onto it when
feat/sn-backend merged the phone app's backend branches (one head).

Revision ID: c0025_push_devices
Revises: c0024_app_sessions
"""

from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

from alembic import op

revision: str = "c0025_push_devices"
down_revision: str | None = "c0024_app_sessions"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.create_table(
        "push_devices",
        sa.Column("id", sa.Integer(), primary_key=True),
        sa.Column(
            "user_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("user.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("token", sa.String(length=255), nullable=False),
        sa.Column("platform", sa.String(length=16), nullable=False),
        sa.Column("label", sa.String(length=120), nullable=True),
        sa.Column("language", sa.String(length=16), nullable=True),
        sa.Column("last_seen_at", sa.TIMESTAMP(timezone=True), nullable=False),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), nullable=False),
    )
    op.create_index("ix_push_devices_id", "push_devices", ["id"], unique=False)
    op.create_index(
        "ix_push_devices_user_id", "push_devices", ["user_id"], unique=False
    )
    op.create_index("ix_push_devices_token", "push_devices", ["token"], unique=True)
    op.create_index(
        "ix_push_devices_created_at", "push_devices", ["created_at"], unique=False
    )

    op.create_table(
        "push_preferences",
        sa.Column(
            "user_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("user.id", ondelete="CASCADE"),
            primary_key=True,
        ),
        sa.Column(
            "choices",
            postgresql.JSONB(astext_type=sa.Text()),
            nullable=False,
            server_default=sa.text("'{}'"),
        ),
        sa.Column("updated_at", sa.TIMESTAMP(timezone=True), nullable=False),
    )

    op.create_table(
        "push_tickets",
        sa.Column("id", sa.Integer(), primary_key=True),
        sa.Column("ticket_id", sa.String(length=64), nullable=False),
        sa.Column("token", sa.String(length=255), nullable=False),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), nullable=False),
    )
    op.create_index("ix_push_tickets_id", "push_tickets", ["id"], unique=False)
    op.create_index(
        "ix_push_tickets_ticket_id", "push_tickets", ["ticket_id"], unique=True
    )
    op.create_index(
        "ix_push_tickets_created_at", "push_tickets", ["created_at"], unique=False
    )


def downgrade() -> None:
    op.drop_index("ix_push_tickets_created_at", table_name="push_tickets")
    op.drop_index("ix_push_tickets_ticket_id", table_name="push_tickets")
    op.drop_index("ix_push_tickets_id", table_name="push_tickets")
    op.drop_table("push_tickets")
    op.drop_table("push_preferences")
    op.drop_index("ix_push_devices_created_at", table_name="push_devices")
    op.drop_index("ix_push_devices_token", table_name="push_devices")
    op.drop_index("ix_push_devices_user_id", table_name="push_devices")
    op.drop_index("ix_push_devices_id", table_name="push_devices")
    op.drop_table("push_devices")
