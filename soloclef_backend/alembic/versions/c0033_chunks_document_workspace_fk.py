"""A chunk's workspace must be its document's -- the one check c0031/c0032 leave out.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0033_chunks_document_ws_fk
Revises: c0032_chunks_ws_id_not_null

Why
---
``chunks.workspace_id`` exists so filtered vector search keeps its HNSW index
(c0031). Two constraints guard it and neither reads the chunk's document:
``chunks_workspace_id_fkey`` only asks that the workspace exists, and
c0032's NOT NULL only that a value is there. A chunk stamped with a *real but
wrong* workspace therefore passes every check and is then invisible to its own
workspace's search and offered to another workspace's -- the corruption
c0031's docstring names, with nothing raised and nothing logged. The hole is
recorded as a passing test in
``tests/integration/test_chunks_workspace_id_c0031_migration.py``; this
revision closes it.

Shape
-----
A composite FK can only point at a unique key, so ``documents`` gets one on
``(id, workspace_id)`` -- redundant with its primary key as a *key*, and that
is the point: it is there to be referenced.

1. ``CREATE UNIQUE INDEX CONCURRENTLY`` on ``documents (id, workspace_id)``,
   outside alembic's transaction, so an index build never blocks ingestion.
2. ``ADD CONSTRAINT ... UNIQUE USING INDEX`` adopts that index as the
   constraint the model declares, which is a catalog edit under a brief
   ACCESS EXCLUSIVE rather than a second, blocking index build.
3. The FK is added ``NOT VALID`` (brief ACCESS EXCLUSIVE, no scan) and
   validated separately (SHARE UPDATE EXCLUSIVE on ``chunks``, which concurrent
   reads and writes tolerate) -- c0031's reasoning, unchanged.

A row that already disagrees fails the VALIDATE rather than being rewritten:
the migration cannot know whether the chunk or the document carries the right
workspace, and c0031 already chose a failed migration over a silent repair.
``SELECT count(*) FROM chunks c JOIN documents d ON d.id = c.document_id WHERE
c.workspace_id IS DISTINCT FROM d.workspace_id`` says in advance whether that
can happen (it is 0 on this server).

``ON UPDATE CASCADE`` because the invariant has to survive a document moving
workspace: without it such an UPDATE would be refused, with it the document's
chunks are restamped with it. ``ON DELETE CASCADE`` to match the existing
``document_id`` FK, which already takes the chunk with its document.

Cost at this scale
------------------
The unique index is one more btree on ``documents`` (149 rows here, two ints
per entry). The FK costs one index probe per chunk INSERT, next to an HNSW
insert on a 1024-dimension vector in the same statement. Nothing else can
catch this corruption, so that is the whole price of closing it.
"""

from __future__ import annotations

from collections.abc import Sequence

from alembic import op

revision: str = "c0033_chunks_document_ws_fk"
down_revision: str | None = "c0032_chunks_ws_id_not_null"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

UNIQUE_INDEX = "documents_id_workspace_id_key"
FK_NAME = "chunks_document_workspace_fkey"


def upgrade() -> None:
    with op.get_context().autocommit_block():
        op.execute(
            f"CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS {UNIQUE_INDEX} "
            "ON documents (id, workspace_id)"
        )
    op.execute(
        f"""
        DO $$
        BEGIN
            IF NOT EXISTS (
                SELECT 1 FROM pg_constraint WHERE conname = '{UNIQUE_INDEX}'
            ) THEN
                ALTER TABLE documents
                    ADD CONSTRAINT {UNIQUE_INDEX}
                    UNIQUE USING INDEX {UNIQUE_INDEX};
            END IF;
            IF NOT EXISTS (
                SELECT 1 FROM pg_constraint WHERE conname = '{FK_NAME}'
            ) THEN
                ALTER TABLE chunks
                    ADD CONSTRAINT {FK_NAME}
                    FOREIGN KEY (document_id, workspace_id)
                    REFERENCES documents (id, workspace_id)
                    ON DELETE CASCADE ON UPDATE CASCADE NOT VALID;
            END IF;
        END$$;
        """
    )
    op.execute(f"ALTER TABLE chunks VALIDATE CONSTRAINT {FK_NAME}")


def downgrade() -> None:
    op.execute(f"ALTER TABLE chunks DROP CONSTRAINT IF EXISTS {FK_NAME}")
    # Dropping the constraint takes its backing index with it.
    op.execute(f"ALTER TABLE documents DROP CONSTRAINT IF EXISTS {UNIQUE_INDEX}")
    with op.get_context().autocommit_block():
        op.execute(f"DROP INDEX CONCURRENTLY IF EXISTS {UNIQUE_INDEX}")
