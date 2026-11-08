"""An announcement remembers the readers who deleted their copy.

``site_announcements.cleared_read_count`` counts the recipients who read their
copy of an announcement and then deleted it from their inbox (one notification
deleted, or "Clear all"). The copy is gone, so it no longer shows in the read
copies the Sent list counts; this keeps "read by N of M" from dropping when a
reader tidies up. Unread copies deleted are not counted.

NOT NULL with a constant default: on PG 11+ that only touches the catalogue,
under a short ``lock_timeout`` so it never queues the backend behind it for
long; if the lock is not had in time the migration fails and can simply be run
again. Existing rows read 0 (nothing could be deleted before this).

Not in the Zero publication (site_announcements never was).

Local revision, namespaced as custom/README.md explains.

Revision ID: c0028_announcement_cleared_reads
Revises: c0027_refresh_token_signed_in_at
"""

from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa

from alembic import op

revision: str = "c0028_announcement_cleared_reads"
down_revision: str | None = "c0027_refresh_token_signed_in_at"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

#: How long this migration waits for site_announcements' lock before giving up.
LOCK_TIMEOUT = "5s"


def upgrade() -> None:
    op.execute(f"SET LOCAL lock_timeout = '{LOCK_TIMEOUT}'")
    op.add_column(
        "site_announcements",
        sa.Column(
            "cleared_read_count",
            sa.Integer(),
            nullable=False,
            server_default="0",
        ),
    )


def downgrade() -> None:
    op.execute(f"SET LOCAL lock_timeout = '{LOCK_TIMEOUT}'")
    op.drop_column("site_announcements", "cleared_read_count")
