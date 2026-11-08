"""Automation runs: a ``needs_attention`` status.

A run whose agent could not do its task because a person has to act (widen
what the automation may use, approve actions, answer a question) now ends as
``needs_attention`` instead of ``succeeded``. Only the enum gains a value; no
row changes.

``ALTER TYPE ... ADD VALUE`` cannot be undone in PostgreSQL, so the downgrade
moves any such runs to ``failed`` and leaves the (unused) value in place.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0016_automation_needs_attention
Revises: c0015_notebook_public_links
"""

from __future__ import annotations

from collections.abc import Sequence

from alembic import op

revision: str = "c0016_automation_needs_attention"
down_revision: str | None = "c0015_notebook_public_links"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    with op.get_context().autocommit_block():
        op.execute(
            "ALTER TYPE automation_run_status ADD VALUE IF NOT EXISTS 'needs_attention'"
        )


def downgrade() -> None:
    op.execute(
        "UPDATE automation_runs SET status = 'failed' WHERE status = 'needs_attention'"
    )
