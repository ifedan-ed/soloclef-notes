"""A refresh token remembers when its sign-in began.

``refresh_tokens.signed_in_at`` is the moment the family (one sign-in, one
device) was made; every rotated token copies it from the token it replaces.
The signed-in devices list (``GET /auth/devices``) dated a sign-in by the
family's earliest row, but the daily purge (``purge_refresh_tokens``, 03:41)
deletes rotated rows once they are past the rotation grace, so after a purge
"signed in" read as the last refresh. Rows from before this have none and are
still dated by their earliest row (``refresh_tokens.devices_from_rows``).

Nullable, no default, no backfill: adding it only touches the catalogue
(PG 11+), under a short ``lock_timeout`` so it never queues the running
backend's refreshes behind it for long; if the lock is not had in time the
migration fails and can simply be run again.

Not in the Zero publication (refresh_tokens never was).

Local revision, namespaced as custom/README.md explains.

Revision ID: c0027_refresh_token_signed_in_at
Revises: c0026_push_device_session
"""

from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa

from alembic import op

revision: str = "c0027_refresh_token_signed_in_at"
down_revision: str | None = "c0026_push_device_session"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

#: How long this migration waits for refresh_tokens' lock before giving up.
LOCK_TIMEOUT = "5s"


def upgrade() -> None:
    op.execute(f"SET LOCAL lock_timeout = '{LOCK_TIMEOUT}'")
    op.add_column(
        "refresh_tokens",
        sa.Column("signed_in_at", sa.TIMESTAMP(timezone=True), nullable=True),
    )


def downgrade() -> None:
    op.execute(f"SET LOCAL lock_timeout = '{LOCK_TIMEOUT}'")
    op.drop_column("refresh_tokens", "signed_in_at")
