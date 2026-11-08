"""Workspaces: the quick-tasks model (Settings -> Models).

One row per workspace that changed the default. No row means the default
quick model (``ds-deepseek-v4.1-flash`` when the workspace can reach it),
``model_id = 0`` means "same as the chat model". A table rather than a
``workspaces`` column so upstream's model keeps its shape; see
custom/README.md for why local revisions are namespaced.

Revision ID: c0004_workspace_quick_model
Revises: c0003_earlier_chats
"""

from collections.abc import Sequence

from alembic import op

revision: str = "c0004_workspace_quick_model"
down_revision: str | None = "c0003_earlier_chats"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.execute(
        """
        CREATE TABLE IF NOT EXISTS workspace_quick_models (
            workspace_id INTEGER PRIMARY KEY
                REFERENCES workspaces(id) ON DELETE CASCADE,
            model_id INTEGER NOT NULL,
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )
        """
    )


def downgrade() -> None:
    op.execute("DROP TABLE IF EXISTS workspace_quick_models")
