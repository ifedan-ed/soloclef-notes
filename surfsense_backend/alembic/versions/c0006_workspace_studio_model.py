"""Workspaces: the Studio outputs model (Settings -> Models).

One row per workspace that changed the recommended setup: a default choice
and per-output-type overrides, as JSON, validated by the application. No row
means the recommended setup (GPT-5.6 Luna with DeepSeek v4.1 Flash behind it,
and the measured per-type picks). A table rather than a ``workspaces`` column
so upstream's model keeps its shape; see custom/README.md for why local
revisions are namespaced.

``notebook_outputs.generated_by`` records which model produced each Studio
output and whether it was the fallback.

Revision ID: c0006_workspace_studio_model
Revises: c0005_nextcloud_connector
"""

from collections.abc import Sequence

from alembic import op

revision: str = "c0006_workspace_studio_model"
down_revision: str | None = "c0005_nextcloud_connector"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.execute(
        """
        CREATE TABLE IF NOT EXISTS workspace_studio_models (
            workspace_id INTEGER PRIMARY KEY
                REFERENCES workspaces(id) ON DELETE CASCADE,
            default_choice JSONB NOT NULL,
            overrides JSONB NOT NULL DEFAULT '{}'::jsonb,
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )
        """
    )
    op.execute(
        "ALTER TABLE notebook_outputs ADD COLUMN IF NOT EXISTS generated_by JSONB"
    )


def downgrade() -> None:
    op.execute("ALTER TABLE notebook_outputs DROP COLUMN IF EXISTS generated_by")
    op.execute("DROP TABLE IF EXISTS workspace_studio_models")
