"""chunks.workspace_id NOT NULL -- the backstop c0031 deliberately deferred.

c0031 added the column, backfilled it and indexed it, but left it nullable:
compose runs the ``migrations`` service to completion while the old containers
are still serving, so setting NOT NULL in that migration would have failed any
chunk INSERT from code that did not yet stamp the column.

That code is live now, so the constraint can go on. It matters because a write
path that forgets the column writes a chunk whose workspace nothing can
determine, and every workspace-scoped search filters on exactly that column --
so the chunk is simply never returned, with no error and nothing logged. NOT
NULL turns that invisible loss into a failed INSERT.

``SET NOT NULL`` on its own takes ACCESS EXCLUSIVE for a full table scan.
Adding the CHECK ``NOT VALID``, validating it (SHARE UPDATE EXCLUSIVE, which
concurrent reads and writes tolerate) and only then setting NOT NULL lets
PG 12+ skip that scan, because the validated CHECK already proves it. The CHECK
is dropped afterwards, having served its purpose.
"""

from collections.abc import Sequence

from alembic import op

revision: str = "c0032_chunks_ws_id_not_null"
down_revision: str | None = "c0031_chunks_workspace_id"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

CHECK_NAME = "chunks_workspace_id_not_null"


def upgrade() -> None:
    # Any row left behind by a write path c0031 missed would fail the VALIDATE
    # below, which is the point: better a failed migration than a silent hole.
    op.execute(
        f"""
        DO $$
        BEGIN
            IF NOT EXISTS (
                SELECT 1 FROM pg_constraint WHERE conname = '{CHECK_NAME}'
            ) THEN
                ALTER TABLE chunks
                    ADD CONSTRAINT {CHECK_NAME}
                    CHECK (workspace_id IS NOT NULL) NOT VALID;
            END IF;
        END$$;
        """
    )
    op.execute(f"ALTER TABLE chunks VALIDATE CONSTRAINT {CHECK_NAME}")
    op.execute("ALTER TABLE chunks ALTER COLUMN workspace_id SET NOT NULL")
    op.execute(f"ALTER TABLE chunks DROP CONSTRAINT {CHECK_NAME}")


def downgrade() -> None:
    op.execute(f"ALTER TABLE chunks DROP CONSTRAINT IF EXISTS {CHECK_NAME}")
    op.execute("ALTER TABLE chunks ALTER COLUMN workspace_id DROP NOT NULL")
