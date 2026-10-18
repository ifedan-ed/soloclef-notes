"""File every standalone chat into its workspace's "Earlier chats" notebook.

The standalone chat UI (New chat, Chats, Recents) is gone: chat lives in
notebooks now. Chats started before that have no notebook, so without this
they would have nowhere to open. Each workspace that has any gets one
notebook, "Earlier chats", holding all of them; messages, artifacts and
citations stay attached to the thread, which only gains a ``notebook_id``.

No thread records the notebook it was started from (a notebook chat already
has ``notebook_id``), so none is guessed into an existing notebook. API-run
threads (``source = 'artifact_api'``) are machinery, not conversations, and
are left alone.

``notebook_earlier_chats`` records every move, which is what makes this
reversible: the downgrade detaches exactly those threads, then removes the
"Earlier chats" notebooks that are left without any chat.

Revision ID: c0003_earlier_chats
Revises: c0002_notebook_chat_settings
"""

from collections.abc import Sequence

from alembic import op

revision: str = "c0003_earlier_chats"
down_revision: str | None = "c0002_notebook_chat_settings"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

# Equal to app.notebooks.earlier_chats.placement, which files chats that
# arrive later (a messaging channel, a copied public chat) the same way.
TITLE = "Earlier chats"
ICON = "\U0001f4ac"

CREATE_TABLE = """
CREATE TABLE IF NOT EXISTS notebook_earlier_chats (
    thread_id INTEGER PRIMARY KEY
        REFERENCES new_chat_threads(id) ON DELETE CASCADE,
    notebook_id INTEGER NOT NULL
        REFERENCES notebooks(id) ON DELETE CASCADE,
    workspace_id INTEGER NOT NULL
        REFERENCES workspaces(id) ON DELETE CASCADE,
    moved_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
"""

CREATE_INDEXES = (
    "CREATE INDEX IF NOT EXISTS ix_notebook_earlier_chats_notebook_id "
    "ON notebook_earlier_chats (notebook_id)",
    "CREATE INDEX IF NOT EXISTS ix_notebook_earlier_chats_workspace_id "
    "ON notebook_earlier_chats (workspace_id)",
)

# One notebook per workspace that has standalone chats, owned by the
# workspace owner, and a record per chat filed into it.
FILE_CHATS = f"""
WITH standalone AS (
    SELECT id, workspace_id
    FROM new_chat_threads
    WHERE notebook_id IS NULL AND source <> 'artifact_api'
),
created AS (
    INSERT INTO notebooks
        (workspace_id, title, icon, created_by_id, created_at, updated_at)
    SELECT w.id, '{TITLE}', '{ICON}', w.user_id, NOW(), NOW()
    FROM workspaces w
    WHERE w.id IN (SELECT DISTINCT workspace_id FROM standalone)
    RETURNING id, workspace_id
)
INSERT INTO notebook_earlier_chats (thread_id, notebook_id, workspace_id)
SELECT s.id, c.id, s.workspace_id
FROM standalone s
JOIN created c ON c.workspace_id = s.workspace_id
"""

ATTACH_CHATS = """
UPDATE new_chat_threads t
SET notebook_id = e.notebook_id
FROM notebook_earlier_chats e
WHERE e.thread_id = t.id AND t.notebook_id IS NULL
"""

DETACH_CHATS = """
UPDATE new_chat_threads t
SET notebook_id = NULL
FROM notebook_earlier_chats e
WHERE e.thread_id = t.id AND t.notebook_id = e.notebook_id
"""

# Only the notebooks this revision created, and only once no chat is left in
# them: a chat the user started there since keeps its notebook.
REMOVE_EMPTY_EARLIER_CHATS = """
DELETE FROM notebooks n
WHERE n.id IN (SELECT notebook_id FROM notebook_earlier_chats)
  AND NOT EXISTS (SELECT 1 FROM new_chat_threads t WHERE t.notebook_id = n.id)
"""

DROP_TABLE = "DROP TABLE IF EXISTS notebook_earlier_chats"

UPGRADE = (CREATE_TABLE, *CREATE_INDEXES, FILE_CHATS, ATTACH_CHATS)
DOWNGRADE = (DETACH_CHATS, REMOVE_EMPTY_EARLIER_CHATS, DROP_TABLE)


def upgrade() -> None:
    for statement in UPGRADE:
        op.execute(statement)


def downgrade() -> None:
    for statement in DOWNGRADE:
        op.execute(statement)
