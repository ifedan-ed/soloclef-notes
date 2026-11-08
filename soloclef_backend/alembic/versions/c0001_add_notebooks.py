"""Notebooks: sources, notes, studio outputs, source guides, report templates.

This is a local migration, not an upstream one, so its revision id is
namespaced (``c0001_notebooks``) instead of taking the next upstream number:
an upstream ``192`` would otherwise collide with it. When upstream adds a
migration on top of ``191`` the history will have two heads and
``alembic upgrade head`` will refuse to run; the fix is a merge revision, see
custom/README.md.

Revision ID: c0001_notebooks
Revises: 191
"""

from collections.abc import Sequence

from alembic import op

revision: str = "c0001_notebooks"
down_revision: str | None = "191"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.execute(
        """
        CREATE TABLE IF NOT EXISTS notebooks (
            id SERIAL PRIMARY KEY,
            workspace_id INTEGER NOT NULL
                REFERENCES workspaces(id) ON DELETE CASCADE,
            title VARCHAR(200) NOT NULL,
            icon VARCHAR(16),
            created_by_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
            focus_suggestions JSONB,
            focus_suggestions_key VARCHAR(64),
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )
        """
    )
    op.execute("CREATE INDEX IF NOT EXISTS ix_notebooks_id ON notebooks (id)")
    op.execute(
        "CREATE INDEX IF NOT EXISTS ix_notebooks_workspace_id ON notebooks (workspace_id)"
    )
    op.execute(
        "CREATE INDEX IF NOT EXISTS ix_notebooks_created_at ON notebooks (created_at)"
    )
    op.execute(
        "CREATE INDEX IF NOT EXISTS ix_notebooks_updated_at ON notebooks (updated_at)"
    )

    op.execute(
        """
        CREATE TABLE IF NOT EXISTS notebook_sources (
            id SERIAL PRIMARY KEY,
            notebook_id INTEGER NOT NULL
                REFERENCES notebooks(id) ON DELETE CASCADE,
            document_id INTEGER REFERENCES documents(id) ON DELETE CASCADE,
            folder_id INTEGER REFERENCES folders(id) ON DELETE CASCADE,
            enabled BOOLEAN NOT NULL DEFAULT true,
            added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            CONSTRAINT ck_notebook_sources_one_target
                CHECK (num_nonnulls(document_id, folder_id) = 1),
            CONSTRAINT uq_notebook_sources_document UNIQUE (notebook_id, document_id),
            CONSTRAINT uq_notebook_sources_folder UNIQUE (notebook_id, folder_id)
        )
        """
    )
    for column in ("id", "notebook_id", "document_id", "folder_id"):
        op.execute(
            f"CREATE INDEX IF NOT EXISTS ix_notebook_sources_{column} "
            f"ON notebook_sources ({column})"
        )

    op.execute(
        """
        CREATE TABLE IF NOT EXISTS notebook_notes (
            id SERIAL PRIMARY KEY,
            notebook_id INTEGER NOT NULL
                REFERENCES notebooks(id) ON DELETE CASCADE,
            title VARCHAR(300) NOT NULL,
            content TEXT NOT NULL DEFAULT '',
            created_by_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
            converted_document_id INTEGER
                REFERENCES documents(id) ON DELETE SET NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )
        """
    )
    for column in ("id", "notebook_id", "created_at"):
        op.execute(
            f"CREATE INDEX IF NOT EXISTS ix_notebook_notes_{column} "
            f"ON notebook_notes ({column})"
        )

    op.execute(
        """
        CREATE TABLE IF NOT EXISTS notebook_outputs (
            id SERIAL PRIMARY KEY,
            notebook_id INTEGER NOT NULL
                REFERENCES notebooks(id) ON DELETE CASCADE,
            workspace_id INTEGER NOT NULL
                REFERENCES workspaces(id) ON DELETE CASCADE,
            kind VARCHAR(32) NOT NULL,
            title VARCHAR(500) NOT NULL,
            options JSONB NOT NULL DEFAULT '{}',
            source_document_ids JSONB NOT NULL DEFAULT '[]',
            podcast_id INTEGER REFERENCES podcast_runs(id) ON DELETE SET NULL,
            deliverable_job_id INTEGER
                REFERENCES deliverable_jobs(id) ON DELETE SET NULL,
            thread_id INTEGER REFERENCES new_chat_threads(id) ON DELETE SET NULL,
            created_by_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            CONSTRAINT ck_notebook_outputs_one_run
                CHECK (num_nonnulls(podcast_id, deliverable_job_id) <= 1)
        )
        """
    )
    for column in ("id", "notebook_id", "workspace_id", "created_at"):
        op.execute(
            f"CREATE INDEX IF NOT EXISTS ix_notebook_outputs_{column} "
            f"ON notebook_outputs ({column})"
        )

    op.execute(
        """
        CREATE TABLE IF NOT EXISTS document_source_guides (
            id SERIAL PRIMARY KEY,
            document_id INTEGER NOT NULL UNIQUE
                REFERENCES documents(id) ON DELETE CASCADE,
            content_hash VARCHAR(128) NOT NULL,
            summary TEXT NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )
        """
    )
    for column in ("id", "created_at"):
        op.execute(
            f"CREATE INDEX IF NOT EXISTS ix_document_source_guides_{column} "
            f"ON document_source_guides ({column})"
        )

    op.execute(
        """
        CREATE TABLE IF NOT EXISTS notebook_report_templates (
            id SERIAL PRIMARY KEY,
            workspace_id INTEGER NOT NULL
                REFERENCES workspaces(id) ON DELETE CASCADE,
            template_key VARCHAR(64) NOT NULL,
            title VARCHAR(200) NOT NULL,
            description VARCHAR(500) NOT NULL DEFAULT '',
            prompt TEXT NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            CONSTRAINT uq_notebook_report_templates_key
                UNIQUE (workspace_id, template_key)
        )
        """
    )
    for column in ("id", "workspace_id", "created_at"):
        op.execute(
            f"CREATE INDEX IF NOT EXISTS ix_notebook_report_templates_{column} "
            f"ON notebook_report_templates ({column})"
        )

    op.execute(
        """
        ALTER TABLE new_chat_threads
            ADD COLUMN IF NOT EXISTS notebook_id INTEGER
                REFERENCES notebooks(id) ON DELETE SET NULL
        """
    )
    op.execute(
        "CREATE INDEX IF NOT EXISTS ix_new_chat_threads_notebook_id "
        "ON new_chat_threads (notebook_id)"
    )


def downgrade() -> None:
    op.execute("DROP INDEX IF EXISTS ix_new_chat_threads_notebook_id")
    op.execute("ALTER TABLE new_chat_threads DROP COLUMN IF EXISTS notebook_id")
    op.execute("DROP TABLE IF EXISTS notebook_report_templates")
    op.execute("DROP TABLE IF EXISTS document_source_guides")
    op.execute("DROP TABLE IF EXISTS notebook_outputs")
    op.execute("DROP TABLE IF EXISTS notebook_notes")
    op.execute("DROP TABLE IF EXISTS notebook_sources")
    op.execute("DROP TABLE IF EXISTS notebooks")
