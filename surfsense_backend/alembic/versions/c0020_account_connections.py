"""Connectors: account-level, enabled everywhere the owner is unless switched off.

The owner's complaint: deleting a workspace deleted the Nextcloud/Google
Drive/Email credentials that happened to live in it, and a new workspace
started with none of them. His decision: "Things are linked to your
account. Automatically apply to every workspace unless you disable it."
Full design: custom/ACCOUNT_CONNECTIONS.md.

``MCP_CONNECTOR`` is explicitly excluded — it stays workspace-scoped exactly
as today (a second agent's concurrent tool-switch review owns that code).

For every ``search_source_connectors`` row whose ``connector_type !=
'MCP_CONNECTOR'``:
  1. A ``search_source_connector_enablements`` row is created for the row's
     *current* workspace, ``enabled=TRUE``, carrying its periodic-indexing
     settings forward so that one workspace keeps running exactly as it
     does today. No row is created for any other workspace the owner
     belongs to — default-on needs none; those workspaces simply gain the
     connection with no indexing configured yet (nothing auto-imports).
  2. The connector row's own ``workspace_id`` is set to NULL (it now
     belongs to the account, not one workspace) and its periodic-indexing
     fields are zeroed (the enablement row is the only place they're read
     from now).

No row is deleted; ``search_source_connectors.id`` values are unchanged, so
``documents.connector_id`` (``ON DELETE CASCADE`` via the connector, not
touched here) stays valid with no rewrite.

Revision ID: c0020_account_connections
Revises: c0019_workspace_payer_billing
"""

from collections.abc import Sequence

import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

from alembic import op

revision: str = "c0020_account_connections"
down_revision: str | None = "c0019_workspace_payer_billing"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.create_table(
        "search_source_connector_enablements",
        sa.Column("id", sa.Integer(), primary_key=True),
        sa.Column(
            "connector_id",
            sa.Integer(),
            sa.ForeignKey("search_source_connectors.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column(
            "workspace_id",
            sa.Integer(),
            sa.ForeignKey("workspaces.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column(
            "enabled_by_user_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("user.id", ondelete="SET NULL"),
            nullable=True,
        ),
        sa.Column(
            "enabled", sa.Boolean(), nullable=False, server_default=sa.true()
        ),
        sa.Column(
            "is_indexable", sa.Boolean(), nullable=False, server_default=sa.false()
        ),
        sa.Column("last_indexed_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column(
            "periodic_indexing_enabled",
            sa.Boolean(),
            nullable=False,
            server_default=sa.false(),
        ),
        sa.Column("indexing_frequency_minutes", sa.Integer(), nullable=True),
        sa.Column("next_scheduled_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("indexing_config", postgresql.JSON(), nullable=True),
        sa.Column(
            "created_at",
            sa.TIMESTAMP(timezone=True),
            nullable=False,
            server_default=sa.text("now()"),
        ),
        sa.Column(
            "updated_at",
            sa.TIMESTAMP(timezone=True),
            nullable=False,
            server_default=sa.text("now()"),
        ),
        sa.UniqueConstraint(
            "connector_id", "workspace_id", name="uq_connector_workspace_enablement"
        ),
    )
    op.create_index(
        "ix_search_source_connector_enablements_connector_id",
        "search_source_connector_enablements",
        ["connector_id"],
    )
    op.create_index(
        "ix_search_source_connector_enablements_workspace_id",
        "search_source_connector_enablements",
        ["workspace_id"],
    )

    # 1. Carry every non-MCP connector's current workspace + periodic state
    #    forward into one enablement row.
    op.execute(
        """
        INSERT INTO search_source_connector_enablements
            (connector_id, workspace_id, enabled_by_user_id, enabled,
             is_indexable, last_indexed_at, periodic_indexing_enabled,
             indexing_frequency_minutes, next_scheduled_at,
             created_at, updated_at)
        SELECT id, workspace_id, user_id, TRUE, is_indexable,
               last_indexed_at, periodic_indexing_enabled,
               indexing_frequency_minutes, next_scheduled_at,
               now(), now()
        FROM search_source_connectors
        WHERE connector_type != 'MCP_CONNECTOR'
        """
    )

    # 2. workspace_id is only guaranteed NOT NULL for MCP_CONNECTOR now —
    #    done before the UPDATE below, which sets it NULL on every other row.
    op.alter_column(
        "search_source_connectors", "workspace_id", nullable=True
    )

    # 3. Detach those rows from any one workspace and clear the now-unused
    #    periodic fields (enablement is the source of truth going forward).
    op.execute(
        """
        UPDATE search_source_connectors
        SET workspace_id = NULL,
            periodic_indexing_enabled = FALSE,
            indexing_frequency_minutes = NULL,
            next_scheduled_at = NULL
        WHERE connector_type != 'MCP_CONNECTOR'
        """
    )

    # 4. Account-level uniqueness for the connectors that just went NULL
    #    (the old constraint doesn't compare NULLs, so it would let two
    #    same-named account connectors through).
    op.execute(
        """
        CREATE UNIQUE INDEX uq_account_connector_type_name
        ON search_source_connectors (user_id, connector_type, name)
        WHERE workspace_id IS NULL
        """
    )


def downgrade() -> None:
    # Refuse if any connector has been enabled in more than one workspace
    # since the migration ran — there would be no single workspace_id left
    # to put back on the connector row.
    conn = op.get_bind()
    multi = conn.execute(
        sa.text(
            """
            SELECT connector_id FROM search_source_connector_enablements
            GROUP BY connector_id
            HAVING count(*) > 1
            LIMIT 1
            """
        )
    ).first()
    if multi is not None:
        raise RuntimeError(
            "Cannot downgrade c0020_account_connections: connector "
            f"{multi[0]} is enabled in more than one workspace. Disable it "
            "down to one workspace first, or restore from backup."
        )

    op.execute("DROP INDEX IF EXISTS uq_account_connector_type_name")

    op.execute(
        """
        UPDATE search_source_connectors c
        SET workspace_id = e.workspace_id,
            periodic_indexing_enabled = e.periodic_indexing_enabled,
            indexing_frequency_minutes = e.indexing_frequency_minutes,
            next_scheduled_at = e.next_scheduled_at
        FROM search_source_connector_enablements e
        WHERE e.connector_id = c.id
        """
    )

    op.alter_column("search_source_connectors", "workspace_id", nullable=False)

    op.drop_index(
        "ix_search_source_connector_enablements_workspace_id",
        table_name="search_source_connector_enablements",
    )
    op.drop_index(
        "ix_search_source_connector_enablements_connector_id",
        table_name="search_source_connector_enablements",
    )
    op.drop_table("search_source_connector_enablements")
