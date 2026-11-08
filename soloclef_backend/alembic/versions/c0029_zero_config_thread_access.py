"""Zero stops sending connector config and learns which threads are private.

Two changes to ``zero_publication`` (``app/zero_publication.py`` is the
canonical shape; this only reconciles to it):

* ``search_source_connectors`` gets a column list without ``config``.
  zero-cache sends every published column of a row to whoever syncs it, and
  the connectors panel synced a workspace's MCP servers, so every member of
  a workspace received their headers and tokens unmasked. The website reads
  config from the REST list (masked) instead.
* ``new_chat_threads`` adds ``visibility`` and ``created_by_id``, so the Zero
  chat queries can keep a private thread's messages, comments and session
  state to its creator, as the REST thread routes do.

zero-cache refuses a client whose schema names a column the publication no
longer has (``SchemaVersionNotSupported``, the whole connection): a phone
app build from before this change still names ``search_source_connectors.
config`` and falls back to polling until its update arrives. Columns added
are harmless to old clients. Order and checks: custom/README.md, "Zero no
longer sends connector config".

A plain ``ALTER PUBLICATION ... SET TABLE`` (zero-cache may resync once). No
table is altered, so no table lock beyond the publication's own catalogue
update.

``downgrade`` restores the previous shape, which publishes ``config`` again:
run it only to undo this release, and rotate any key that synced meanwhile.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0029_zero_config_thread_access
Revises: c0028_announcement_cleared_reads
"""

from __future__ import annotations

from collections.abc import Sequence

from alembic import op
from app.zero_publication import ZERO_PUBLICATION, apply_publication

revision: str = "c0029_zero_config_thread_access"
down_revision: str | None = "c0028_announcement_cleared_reads"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

#: The two entries as they were before this revision.
PREVIOUS_SHAPE = {
    # Every column, config included.
    "search_source_connectors": None,
    "new_chat_threads": ["id", "workspace_id"],
}


def upgrade() -> None:
    apply_publication(op.get_bind())


def downgrade() -> None:
    apply_publication(op.get_bind(), {**ZERO_PUBLICATION, **PREVIOUS_SHAPE})
