"""Notebooks: who chose the title and icon, for automatic naming.

A new notebook starts as "Untitled notebook" (in the creator's UI language)
and names itself from its first ready source or its first question until the
user renames it. ``title_source`` and ``icon_source`` record that explicitly:
``auto`` while the app may still change it, ``user`` for good once the user
has. ``auto_title`` holds the bookkeeping of automatic naming (how many times,
when, and from how many sources), so a refinement is bounded.

Existing notebooks whose title is exactly one of the six localized defaults
count as automatically named (nobody chose that text); every other notebook
counts as named by the user.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0008_notebook_title_source
Revises: c0007_long_tool_call_ids
"""

from collections.abc import Sequence

from alembic import op

revision: str = "c0008_notebook_title_source"
down_revision: str | None = "c0007_long_tool_call_ids"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

# A frozen copy of app.notebooks.autoname.defaults.DEFAULT_TITLES.
_DEFAULT_TITLES = (
    "Untitled notebook",
    "Cuaderno sin título",
    "Caderno sem título",
    "बिना शीर्षक की नोटबुक",
    "제목 없는 노트북",
    "未命名笔记本",
)


def upgrade() -> None:
    for column in ("title_source", "icon_source"):
        op.execute(
            f"ALTER TABLE notebooks ADD COLUMN IF NOT EXISTS {column} "
            "VARCHAR(8) NOT NULL DEFAULT 'user'"
        )
        op.execute(
            f"""
            DO $$ BEGIN
                ALTER TABLE notebooks ADD CONSTRAINT ck_notebooks_{column}
                    CHECK ({column} IN ('auto', 'user'));
            EXCEPTION WHEN duplicate_object THEN NULL;
            END $$
            """
        )
    op.execute("ALTER TABLE notebooks ADD COLUMN IF NOT EXISTS auto_title JSONB")
    titles = ", ".join("'" + t.replace("'", "''") + "'" for t in _DEFAULT_TITLES)
    op.execute(
        f"UPDATE notebooks SET title_source = 'auto', icon_source = 'auto' "
        f"WHERE title IN ({titles})"
    )


def downgrade() -> None:
    op.execute("ALTER TABLE notebooks DROP COLUMN IF EXISTS auto_title")
    for column in ("icon_source", "title_source"):
        op.execute(
            f"ALTER TABLE notebooks DROP CONSTRAINT IF EXISTS ck_notebooks_{column}"
        )
        op.execute(f"ALTER TABLE notebooks DROP COLUMN IF EXISTS {column}")
