"""Give chunks their own workspace_id so filtered vector search keeps its index.

Local revision, namespaced as custom/README.md explains, so an upstream 192
cannot collide with it.

Revision ID: c0031_chunks_workspace_id
Revises: c0030_verify_sso_linked_accounts

Why
---
Workspace-scoped chunk search ordered by ``embedding <=> :q`` had its filter on
``documents`` and its HNSW index on ``chunks``. Postgres cannot combine the
two: it either walks ``chucks_vector_index`` discarding other workspaces' rows
until the scan's candidate budget runs out -- returning *fewer* rows than the
query asked for, silently -- or abandons the index for a full distance scan.
With ``workspace_id`` on ``chunks`` the predicate sits on the indexed table, so
the planner can push it through an iterative HNSW scan or read one workspace's
chunks exactly when that is cheaper.

Shape
-----
Written for a ``chunks`` table far larger than today's (5k rows), because that
is the table this change exists for:

1. ``ADD COLUMN`` nullable, with no default -- a metadata-only change in
   PG 11+, no table rewrite.
2. Backfill in bounded id ranges inside an ``autocommit_block``, so each batch
   commits on its own and no single transaction holds row locks over the whole
   table. A partial run is safe to repeat: every step is idempotent.
3. ``CREATE INDEX CONCURRENTLY`` outside alembic's transaction: an index build
   on ``chunks`` must not block ingestion.
4. (moved) ``SET NOT NULL`` -- see "Step 5, on the next deploy" below. It was
   split out of this migration because `migrations` completes while the old
   containers are still up, and the old code does not stamp the column.

Step 5, on the next deploy
--------------------------
Once the stamping code is live, a follow-up migration must add the backstop,
or a write path that forgets the column writes a chunk no search can find::

    ALTER TABLE chunks ADD CONSTRAINT chunks_workspace_id_not_null
        CHECK (workspace_id IS NOT NULL) NOT VALID;
    ALTER TABLE chunks VALIDATE CONSTRAINT chunks_workspace_id_not_null;
    ALTER TABLE chunks ALTER COLUMN workspace_id SET NOT NULL;
    ALTER TABLE chunks DROP CONSTRAINT chunks_workspace_id_not_null;

Why that dance rather than a bare ``SET NOT NULL``
   takes ACCESS EXCLUSIVE for a full table scan; adding the CHECK ``NOT VALID``
   (brief ACCESS EXCLUSIVE), validating it (SHARE UPDATE EXCLUSIVE, concurrent
   with writes) and only then setting NOT NULL lets PG 12+ skip that scan. The
   CHECK is dropped afterwards, having served its purpose.
5. The FK is added ``NOT VALID`` and validated separately, for the same reason.

A row whose ``workspace_id`` disagrees with its document's is invisible to its
own workspace's search and offered to another's, so step 4 is not tidiness:
NOT NULL is what makes a write path that forgets to stamp the column fail at
the INSERT instead of corrupting retrieval.

The backfill UPDATE is not cheap on a large table: ``chunks`` carries an HNSW
index on ``embedding``, and updating a row writes a new tuple version that
every index on the table must point at, that one included. The batching is
what keeps it bounded, and why the batches commit instead of accumulating into
one table-sized transaction.

Deploy order
------------
Compose runs ``migrations`` to completion before the new backend and workers
start, while the *previous* containers are still up, so between step 4 and
their restart a chunk INSERT from the old code carries no ``workspace_id`` and
fails. That path is a Celery indexing task: it records a failed document status
and the next sync retries it. Ship this migration with the code that matches
it, behind ``scripts/deploy_gate.py --wait`` as every deploy here is, and
nothing longer-lived is affected.
"""

from __future__ import annotations

import sys
from collections.abc import Sequence

import sqlalchemy as sa

from alembic import op

revision: str = "c0031_chunks_workspace_id"
down_revision: str | None = "c0030_verify_sso_linked_accounts"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

INDEX_NAME = "ix_chunks_workspace_id"
FK_NAME = "chunks_workspace_id_fkey"
CHECK_NAME = "chunks_workspace_id_not_null"
# Rows per backfill transaction. Measured at ~175 rows/second on a synthetic
# 60k-row, 1024-dim corpus, because each updated row has to be re-pointed from
# the HNSW index. 5000 is therefore about half a minute of row locks per batch
# -- short enough not to stall a concurrent re-index -- while still only a
# couple of thousand statements for a 10M-row table.
BATCH_SIZE = 5_000


def _backfill(conn) -> None:
    """Fill ``workspace_id`` from each chunk's document, one id range per commit.

    Ranges rather than ``WHERE workspace_id IS NULL LIMIT n``: the latter
    re-scans the already-filled head of the table on every batch, which turns
    quadratic once the table is large.
    """
    low, high = conn.execute(
        sa.text("SELECT min(id), max(id) FROM chunks WHERE workspace_id IS NULL")
    ).one()

    if low is None:
        print("chunks.workspace_id: nothing to backfill.")
        return

    print(f"chunks.workspace_id: backfilling ids {low:,}..{high:,}")
    filled = 0
    start = low
    while start <= high:
        stop = start + BATCH_SIZE - 1
        result = conn.execute(
            sa.text(
                """
                UPDATE chunks
                   SET workspace_id = documents.workspace_id
                  FROM documents
                 WHERE chunks.document_id = documents.id
                   AND chunks.workspace_id IS NULL
                   AND chunks.id BETWEEN :start AND :stop
                """
            ),
            {"start": start, "stop": stop},
        )
        filled += result.rowcount or 0
        sys.stdout.write(
            f"\r  chunks.workspace_id: {filled:,} rows "
            f"(through id {min(stop, high):,}/{high:,})"
        )
        sys.stdout.flush()
        start = stop + 1
    print()

    # ON DELETE CASCADE means a chunk without a document cannot exist, so a
    # row still NULL here is a real inconsistency, not a race. It would fail
    # the NOT NULL below anyway; say why.
    orphans = conn.execute(
        sa.text("SELECT count(*) FROM chunks WHERE workspace_id IS NULL")
    ).scalar_one()
    if orphans:
        raise RuntimeError(
            f"{orphans} chunks still have no workspace_id after the backfill; "
            "they reference no document. Resolve them before re-running "
            "this migration."
        )


def upgrade() -> None:
    op.execute("ALTER TABLE chunks ADD COLUMN IF NOT EXISTS workspace_id INTEGER")

    # Each batch is its own transaction, and the index build cannot run in one
    # at all, so both happen outside alembic's.
    with op.get_context().autocommit_block():
        conn = op.get_bind()
        _backfill(conn)
        op.execute(
            f"CREATE INDEX CONCURRENTLY IF NOT EXISTS {INDEX_NAME} "
            "ON chunks (workspace_id)"
        )

    op.execute(
        f"""
        DO $$
        BEGIN
            IF NOT EXISTS (
                SELECT 1 FROM pg_constraint WHERE conname = '{FK_NAME}'
            ) THEN
                ALTER TABLE chunks
                    ADD CONSTRAINT {FK_NAME}
                    FOREIGN KEY (workspace_id) REFERENCES workspaces(id)
                    ON DELETE CASCADE NOT VALID;
            END IF;
        END$$;
        """
    )
    op.execute(f"ALTER TABLE chunks VALIDATE CONSTRAINT {FK_NAME}")

    # NOT NULL is deliberately NOT set here. See "Step 5, on the next deploy"
    # in this module's docstring: compose runs the `migrations` service to
    # completion while the old containers are still serving, so setting it now
    # would fail any chunk INSERT from code that does not yet stamp the column.
    # The column stays nullable through this deploy; the follow-up migration
    # adds the constraint once the stamping code is live.


def downgrade() -> None:
    op.execute(f"ALTER TABLE chunks DROP CONSTRAINT IF EXISTS {CHECK_NAME}")
    op.execute(f"ALTER TABLE chunks DROP CONSTRAINT IF EXISTS {FK_NAME}")
    with op.get_context().autocommit_block():
        op.execute(f"DROP INDEX CONCURRENTLY IF EXISTS {INDEX_NAME}")
    op.execute("ALTER TABLE chunks DROP COLUMN IF EXISTS workspace_id")
