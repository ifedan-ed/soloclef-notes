"""Two sibling folders cannot share a name -- the index three routes already catch.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0034_folder_sibling_name_uq
Revises: c0033_chunks_document_ws_fk

Why
---
``create_folder``, ``update_folder`` and ``move_folder`` each catch
``uq_folder_space_parent_name`` and answer 409, "A folder with this name
already exists at this location". That index does not exist. Migration 109
created it in raw SQL, but a database born since then is built by
``Base.metadata.create_all`` and stamped (``alembic/env.py``'s fresh-database
fast-forward), and the model never declared it -- so the live ``folders`` table
carries nine indexes and none of them is this one. The three 409 branches have
been dead ever since, and duplicate sibling names are accepted.

They must not be. A folder's path in the knowledge store is built from its
name, and nothing disambiguates a collision:
``app/knowledge_store/paths/legacy.py:_build_folder_paths`` walks the parent
chain joining ``safe_folder_segment(name)``, with none of the id suffixing
``doc_to_virtual_path`` gives a document whose title is taken. Two siblings
called "Reports" therefore occupy the *same* ``/documents/Reports`` path, so
``folder_virtual_path`` answers the same path for both rows -- which makes a
rename of one record a git move of the other's subtree, and a delete of one
remove the other's folder from git. The constraint is what keeps a folder row
and a store path one-to-one.

Shape
-----
c0031/c0032/c0033's shape, for the same reasons:

1. ``CREATE UNIQUE INDEX CONCURRENTLY`` outside alembic's transaction, so the
   build never blocks folder writes.
2. The expression ``coalesce(parent_id, 0)`` rather than a plain
   ``UniqueConstraint``: ``parent_id`` is NULL for a top-level folder and
   Postgres lets NULLs repeat in a unique key, so a bare
   ``(workspace_id, parent_id, name)`` would exempt every root folder -- which
   is migration 109's own reasoning, kept.
3. The build is validated: ``CREATE INDEX CONCURRENTLY`` leaves an **invalid**
   index behind when it fails rather than raising at the statement, so the
   catalog is read afterwards and an invalid index is dropped before this
   migration refuses.

Existing duplicates fail it
---------------------------
A duplicate pair cannot be repaired by a migration: which of the two rows
should be renamed, and to what, is the workspace's decision, not this file's.
So duplicates are counted *first* and the migration stops with the list rather
than attempting a build that would fail halfway. ::

    SELECT workspace_id, coalesce(parent_id, 0) AS parent, name, count(*)
      FROM folders
     GROUP BY 1, 2, 3
    HAVING count(*) > 1;

That is 0 rows on this server (``folders`` holds one row). A deployment where
it is not: rename one of each pair -- the UI's rename is the same operation,
and after this migration it answers 409 instead of making another.
"""

from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa

from alembic import op

revision: str = "c0034_folder_sibling_name_uq"
down_revision: str | None = "c0033_chunks_document_ws_fk"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

INDEX_NAME = "uq_folder_space_parent_name"

_DUPLICATES = sa.text(
    """
    SELECT workspace_id, coalesce(parent_id, 0) AS parent, name, count(*) AS rows
      FROM folders
     GROUP BY 1, 2, 3
    HAVING count(*) > 1
     ORDER BY 1, 2, 3
    """
)


def _refuse_on_duplicates(conn) -> None:
    rows = conn.execute(_DUPLICATES).all()
    if not rows:
        return
    listed = "; ".join(
        f"workspace {row.workspace_id}, parent {row.parent}, "
        f"name {row.name!r} ({row.rows} rows)"
        for row in rows
    )
    raise RuntimeError(
        f"{len(rows)} folder name(s) are duplicated among siblings and would "
        f"fail {INDEX_NAME}: {listed}. Rename one of each before re-running "
        "this migration; which row keeps the name is not a decision a "
        "migration can make."
    )


def _is_valid(conn) -> bool:
    return bool(
        conn.execute(
            sa.text(
                """
                SELECT indisvalid
                  FROM pg_index
                  JOIN pg_class ON pg_class.oid = pg_index.indexrelid
                 WHERE pg_class.relname = :name
                """
            ),
            {"name": INDEX_NAME},
        ).scalar()
    )


def upgrade() -> None:
    conn = op.get_bind()
    _refuse_on_duplicates(conn)

    with op.get_context().autocommit_block():
        conn = op.get_bind()
        op.execute(
            f"CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS {INDEX_NAME} "
            "ON folders (workspace_id, coalesce(parent_id, 0), name)"
        )
        if not _is_valid(conn):
            # A failed concurrent build leaves the index in place and invalid,
            # where it costs every write and enforces nothing.
            op.execute(f"DROP INDEX CONCURRENTLY IF EXISTS {INDEX_NAME}")
            raise RuntimeError(
                f"{INDEX_NAME} was built invalid and has been dropped; "
                "the duplicates it refused were created while this migration ran."
            )


def downgrade() -> None:
    with op.get_context().autocommit_block():
        op.execute(f"DROP INDEX CONCURRENTLY IF EXISTS {INDEX_NAME}")
