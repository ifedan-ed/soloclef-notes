"""Notebooks: chat settings (goal, instructions, length) and featured pins.

Local migration on top of ``c0001_notebooks``; see custom/README.md for why
local revisions are namespaced.

Revision ID: c0002_notebook_chat_settings
Revises: c0001_notebooks
"""

from collections.abc import Sequence

from alembic import op

revision: str = "c0002_notebook_chat_settings"
down_revision: str | None = "c0001_notebooks"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.execute(
        """
        ALTER TABLE notebooks
            ADD COLUMN IF NOT EXISTS chat_goal VARCHAR(16)
                NOT NULL DEFAULT 'default',
            ADD COLUMN IF NOT EXISTS chat_instructions TEXT,
            ADD COLUMN IF NOT EXISTS response_length VARCHAR(16)
                NOT NULL DEFAULT 'default'
        """
    )
    op.execute(
        """
        ALTER TABLE notebooks
            ADD CONSTRAINT ck_notebooks_chat_goal
                CHECK (chat_goal IN ('default', 'learning_guide', 'custom')),
            ADD CONSTRAINT ck_notebooks_response_length
                CHECK (response_length IN ('default', 'longer', 'shorter'))
        """
    )

    op.execute(
        """
        CREATE TABLE IF NOT EXISTS notebook_features (
            id SERIAL PRIMARY KEY,
            notebook_id INTEGER NOT NULL
                REFERENCES notebooks(id) ON DELETE CASCADE,
            user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
            featured_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            CONSTRAINT uq_notebook_features_user UNIQUE (notebook_id, user_id)
        )
        """
    )
    op.execute(
        "CREATE INDEX IF NOT EXISTS ix_notebook_features_notebook_id "
        "ON notebook_features (notebook_id)"
    )
    op.execute(
        "CREATE INDEX IF NOT EXISTS ix_notebook_features_user_id "
        "ON notebook_features (user_id)"
    )


def downgrade() -> None:
    op.execute("DROP TABLE IF EXISTS notebook_features")
    op.execute(
        """
        ALTER TABLE notebooks
            DROP CONSTRAINT IF EXISTS ck_notebooks_response_length,
            DROP CONSTRAINT IF EXISTS ck_notebooks_chat_goal,
            DROP COLUMN IF EXISTS response_length,
            DROP COLUMN IF EXISTS chat_instructions,
            DROP COLUMN IF EXISTS chat_goal
        """
    )
