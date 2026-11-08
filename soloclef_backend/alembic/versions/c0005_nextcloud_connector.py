"""Connectors: the Nextcloud file picker (NEXTCLOUD_CONNECTOR).

Files picked in Nextcloud are imported once as ordinary workspace documents
(type FILE), so only the connector type needs a new enum value. Local revision,
namespaced as custom/README.md explains.

Revision ID: c0005_nextcloud_connector
Revises: c0004_workspace_quick_model
"""

from collections.abc import Sequence

from alembic import op

revision: str = "c0005_nextcloud_connector"
down_revision: str | None = "c0004_workspace_quick_model"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.execute(
        "ALTER TYPE searchsourceconnectortype ADD VALUE IF NOT EXISTS 'NEXTCLOUD_CONNECTOR'"
    )


def downgrade() -> None:
    # PostgreSQL cannot drop an enum value; delete the connectors instead so
    # nothing references it, and leave the unused value in place.
    op.execute(
        "DELETE FROM search_source_connectors "
        "WHERE connector_type = 'NEXTCLOUD_CONNECTOR'"
    )
