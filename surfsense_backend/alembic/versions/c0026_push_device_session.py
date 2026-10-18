"""A phone's push token remembers the sign-in it was registered under.

``push_devices.session_family_id`` is the refresh-token family
(``refresh_tokens.family_id``) of the phone's sign-in. A device takes pushes
only while that family has a live token, and its row is deleted when the
family is revoked (one device signed out, sign out everywhere, a password
change), so a phone the server signed out stops getting that account's
pushes even if it never runs its own sign-out (``app/push/store.py``,
``app/utils/refresh_tokens.py``). Rows registered before this (no family)
keep the old rule: they go at sign-out or on Expo's DeviceNotRegistered.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0026_push_device_session
Revises: c0025_push_devices
"""

from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

from alembic import op

revision: str = "c0026_push_device_session"
down_revision: str | None = "c0025_push_devices"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.add_column(
        "push_devices",
        sa.Column("session_family_id", postgresql.UUID(as_uuid=True), nullable=True),
    )
    op.create_index(
        "ix_push_devices_session_family_id",
        "push_devices",
        ["session_family_id"],
        unique=False,
    )


def downgrade() -> None:
    op.drop_index("ix_push_devices_session_family_id", table_name="push_devices")
    op.drop_column("push_devices", "session_family_id")
