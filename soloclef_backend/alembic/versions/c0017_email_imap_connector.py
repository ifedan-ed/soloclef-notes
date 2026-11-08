"""Connectors: Email (IMAP) (EMAIL_IMAP_CONNECTOR).

Read-only, live connector: messages are never bulk-imported as documents, so
only the connector type needs a new enum value. Local revision, namespaced as
custom/README.md explains.

Revision ID: c0017_email_imap_connector
Revises: c0016_automation_needs_attention
"""

from collections.abc import Sequence

from alembic import op

revision: str = "c0017_email_imap_connector"
down_revision: str | None = "c0016_automation_needs_attention"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.execute(
        "ALTER TYPE searchsourceconnectortype ADD VALUE IF NOT EXISTS "
        "'EMAIL_IMAP_CONNECTOR'"
    )


def downgrade() -> None:
    # PostgreSQL cannot drop an enum value; delete the connectors instead so
    # nothing references it, and leave the unused value in place.
    op.execute(
        "DELETE FROM search_source_connectors "
        "WHERE connector_type = 'EMAIL_IMAP_CONNECTOR'"
    )
