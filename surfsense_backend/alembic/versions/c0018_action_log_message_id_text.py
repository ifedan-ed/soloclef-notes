"""agent_action_log.message_id: widen to TEXT, like its siblings.

``c0007_long_tool_call_ids`` widened ``agent_action_log.tool_call_id`` and
``turn_id`` to TEXT because Gemini tool-call ids carry a thought signature
well past 255 chars. It missed that ``ActionLogMiddleware._resolve_message_id``
also stores the same tool-call id in ``message_id`` (still ``VARCHAR(128)`` at
the time), so a Gemini turn logs fine via ``turn_id``/``tool_call_id`` but
fails the whole INSERT with asyncpg's
``StringDataRightTruncationError: value too long for type character
varying(128)`` as soon as ``message_id`` is populated for the same row —
observed 2026-10-01 in soloclef-backend-1 (thread 117). The insert failure is
swallowed by the middleware's defensive try/except, so the chat turn itself
still completes; only the audit-log row for that tool call is lost.

The existing plain btree index on ``message_id`` is replaced with a hash
index for the same reason as c0007: a long thought signature can exceed a
btree entry's ~2.7 kB cap, and equality is the only lookup this column
serves.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0018_action_log_message_id_text
Revises: c0017_email_imap_connector
"""

from collections.abc import Sequence

from alembic import op

revision: str = "c0018_action_log_message_id_text"
down_revision: str | None = "c0017_email_imap_connector"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

_TABLE = "agent_action_log"
_COLUMN = "message_id"
_OLD_LENGTH = 128
_INDEX = "ix_agent_action_log_message_id"


def upgrade() -> None:
    op.execute(f"DROP INDEX IF EXISTS {_INDEX}")
    op.execute(f"ALTER TABLE {_TABLE} ALTER COLUMN {_COLUMN} TYPE TEXT")
    op.execute(f"CREATE INDEX {_INDEX} ON {_TABLE} USING hash ({_COLUMN})")


def downgrade() -> None:
    # An id longer than the old limit cannot go back; the cast fails loudly
    # rather than truncating an id something still looks up.
    op.execute(f"DROP INDEX IF EXISTS {_INDEX}")
    op.execute(
        f"ALTER TABLE {_TABLE} ALTER COLUMN {_COLUMN} TYPE VARCHAR({_OLD_LENGTH})"
    )
    op.execute(f"CREATE INDEX {_INDEX} ON {_TABLE} ({_COLUMN})")
