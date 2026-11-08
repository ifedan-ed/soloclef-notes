"""Tool-call ids of any length: Gemini's carry a thought signature.

Gemini returns tool-call ids with a ``__thought__`` signature appended, often
well over 255 characters, and saving a deliverable job or an artifact for such
a call failed with "value too long for type character varying(255)". Every
column that stores a LangChain tool-call id becomes TEXT.

A btree entry is capped near 2.7 kB, which a long signature can exceed, so the
plain lookup indexes on these columns become hash indexes (equality is the only
lookup any of them serves) and the deliverable-job idempotency key compares an
md5 of the id instead of the id itself.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0007_long_tool_call_ids
Revises: c0006_workspace_studio_model
"""

from collections.abc import Sequence

from alembic import op

revision: str = "c0007_long_tool_call_ids"
down_revision: str | None = "c0006_workspace_studio_model"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

# (table, column, previous varchar length, lookup index or None)
_COLUMNS = (
    ("deliverable_jobs", "tool_call_id", 255, None),
    ("artifacts", "created_by_tool_call_id", 255, None),
    ("artifacts", "updated_by_tool_call_id", 255, None),
    ("agent_action_log", "tool_call_id", 64, "ix_agent_action_log_tool_call_id"),
    # ``turn_id`` still receives the tool-call id for one release.
    ("agent_action_log", "turn_id", 64, "ix_agent_action_log_turn_id"),
    (
        "document_revisions",
        "created_by_turn_id",
        64,
        "ix_document_revisions_created_by_turn_id",
    ),
    (
        "folder_revisions",
        "created_by_turn_id",
        64,
        "ix_folder_revisions_created_by_turn_id",
    ),
)
_JOB_KEY = "uq_deliverable_jobs_workspace_kind_tool_call"


def upgrade() -> None:
    op.execute(f"ALTER TABLE deliverable_jobs DROP CONSTRAINT IF EXISTS {_JOB_KEY}")
    op.execute(f"DROP INDEX IF EXISTS {_JOB_KEY}")
    for table, column, _length, index in _COLUMNS:
        if index:
            op.execute(f"DROP INDEX IF EXISTS {index}")
        op.execute(f"ALTER TABLE {table} ALTER COLUMN {column} TYPE TEXT")
        if index:
            op.execute(f"CREATE INDEX {index} ON {table} USING hash ({column})")
    op.execute(
        f"CREATE UNIQUE INDEX {_JOB_KEY} ON deliverable_jobs "
        "(workspace_id, kind, md5(tool_call_id))"
    )


def downgrade() -> None:
    # Ids longer than the old limits cannot go back; the cast fails loudly
    # rather than truncating an id that something still looks up.
    op.execute(f"DROP INDEX IF EXISTS {_JOB_KEY}")
    for table, column, length, index in _COLUMNS:
        if index:
            op.execute(f"DROP INDEX IF EXISTS {index}")
        op.execute(f"ALTER TABLE {table} ALTER COLUMN {column} TYPE VARCHAR({length})")
        if index:
            op.execute(f"CREATE INDEX {index} ON {table} ({column})")
    op.execute(
        f"ALTER TABLE deliverable_jobs ADD CONSTRAINT {_JOB_KEY} "
        "UNIQUE (workspace_id, kind, tool_call_id)"
    )
