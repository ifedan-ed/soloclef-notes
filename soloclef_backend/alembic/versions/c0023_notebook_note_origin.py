"""Notebook notes: where each one came from, and which automation wrote it.

The Notes page lists every note in a workspace and says how each came to be.
``notebook_notes.origin`` is ``hand`` (Add note), ``chat`` (Save to note under
an answer) or ``automation`` (an automation's "Save results to notebook");
``notebook_notes.automation_id`` names the automation, and a deleted
automation only clears it (ON DELETE SET NULL), keeping the note.

Existing rows become ``hand`` with no automation. Notes that automations
already wrote are labelled by ``scripts/attach_automation_notes.py``, which
the owner runs by hand (custom/README.md, "Notes page"); this migration
changes no row beyond the default.

Admin data served over REST: not added to the Zero publication.

Local revision, namespaced as custom/README.md explains. It was written on
c0021_scrape_crawl_billing beside feat/chat-search-tools'
c0022_chat_web_search_prices; the release that merged both re-chained it onto
c0022, so the history has one head (custom/README.md, "Local migrations";
tests/unit/db/test_alembic_heads.py).

Revision ID: c0023_notebook_note_origin
Revises: c0022_chat_web_search_prices
"""

from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa

from alembic import op

revision: str = "c0023_notebook_note_origin"
down_revision: str | None = "c0022_chat_web_search_prices"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.add_column(
        "notebook_notes",
        sa.Column(
            "origin",
            sa.String(length=16),
            nullable=False,
            server_default="hand",
        ),
    )
    op.create_check_constraint(
        "ck_notebook_notes_origin",
        "notebook_notes",
        "origin IN ('hand', 'chat', 'automation')",
    )
    op.add_column(
        "notebook_notes",
        sa.Column(
            "automation_id",
            sa.Integer(),
            sa.ForeignKey(
                "automations.id",
                ondelete="SET NULL",
                name="notebook_notes_automation_id_fkey",
            ),
            nullable=True,
        ),
    )
    op.create_index(
        "ix_notebook_notes_automation_id",
        "notebook_notes",
        ["automation_id"],
        unique=False,
    )


def downgrade() -> None:
    op.drop_index("ix_notebook_notes_automation_id", table_name="notebook_notes")
    op.drop_constraint(
        "notebook_notes_automation_id_fkey", "notebook_notes", type_="foreignkey"
    )
    op.drop_column("notebook_notes", "automation_id")
    op.drop_constraint("ck_notebook_notes_origin", "notebook_notes", type_="check")
    op.drop_column("notebook_notes", "origin")
