"""The phone app's sessions: a refresh token's kind and its device label.

``refresh_tokens.kind`` is ``web`` (a browser or the desktop app, the sliding
``REFRESH_TOKEN_LIFETIME_SECONDS`` / ``REFRESH_ABSOLUTE_LIFETIME_SECONDS``
rule) or ``app`` (the Soloclef phone app: one token family per device,
``APP_SESSION_LIFETIME_SECONDS`` from the sign-in, no idle timeout).
``device_label`` is what the phone calls itself ("iPhone 15 Pro · Soloclef
app"), shown in the account's signed-in devices list (``GET /auth/devices``).
Existing rows become ``web`` with no label. ``app/utils/refresh_tokens.py``.

Not in the Zero publication (refresh_tokens never was).

Locks: both ADD COLUMNs only touch the catalogue (a constant default, PG 11+),
but the CHECK constraint is validated by scanning refresh_tokens under the
ACCESS EXCLUSIVE lock the first ADD COLUMN took. The table is small (the
daily ``purge_refresh_tokens`` keeps only live tokens and the 45 s rotation
grace), so the scan is short; ``lock_timeout`` (``LOCK_TIMEOUT``) bounds how
long the running backend's refreshes can queue behind a migration waiting for
that lock. Not had in time, the migration fails and is simply run again.

Local revision, namespaced as custom/README.md explains ("Local migrations").
Written on c0023_notebook_note_origin beside other feat/sn-* branches: if two
of them add a c0024, the release that merges them re-chains one onto the
other (tests/unit/db/test_alembic_heads.py fails until there is one head).

Revision ID: c0024_app_sessions
Revises: c0023_notebook_note_origin
"""

from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa

from alembic import op

revision: str = "c0024_app_sessions"
down_revision: str | None = "c0023_notebook_note_origin"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

#: How long this migration waits for refresh_tokens' lock before giving up.
LOCK_TIMEOUT = "5s"


def upgrade() -> None:
    op.execute(f"SET LOCAL lock_timeout = '{LOCK_TIMEOUT}'")
    op.add_column(
        "refresh_tokens",
        sa.Column("kind", sa.String(length=16), nullable=False, server_default="web"),
    )
    op.add_column(
        "refresh_tokens",
        sa.Column("device_label", sa.String(length=100), nullable=True),
    )
    op.create_check_constraint(
        "ck_refresh_tokens_kind", "refresh_tokens", "kind IN ('web', 'app')"
    )


def downgrade() -> None:
    op.execute(f"SET LOCAL lock_timeout = '{LOCK_TIMEOUT}'")
    op.drop_constraint("ck_refresh_tokens_kind", "refresh_tokens", type_="check")
    op.drop_column("refresh_tokens", "device_label")
    op.drop_column("refresh_tokens", "kind")
