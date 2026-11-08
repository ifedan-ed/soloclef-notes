"""Workspace-owner billing: who actually pays for a metered action.

Today ``meter.begin`` always locks and debits the *acting* user's own
``billing_accounts`` row; ``workspace_id`` on ``billing_usage`` is only a
label. The owner wants an invited member's usage inside *his* workspace to
spend *his* credits, like a registered member using the owner's plan.

``billing_usage.payer_user_id`` records who was actually charged (the
workspace owner for a workspace-scoped action, the acting user otherwise),
kept alongside the existing ``user_id`` (the actor) so a usage row always
shows both "who did this" and "who paid for it". Backfilled to ``user_id``
for every existing row, so old rows keep reading as self-paid. It's internal
bookkeeping only for now -- nothing outside ``app.billing.meter`` reads it.

Local revision, namespaced as custom/README.md explains (upstream has no
``c00NN`` numbers). Picked c0019 as the next free local number after c0018
on 2026-10-01; another agent's branch (account-level connectors) may also
claim a ``c00NN`` number around the same time — if both land, resolve with
an Alembic merge revision rather than renumbering either one.

Revision ID: c0019_workspace_payer_billing
Revises: c0018_action_log_message_id_text
"""

from collections.abc import Sequence

import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

from alembic import op

revision: str = "c0019_workspace_payer_billing"
down_revision: str | None = "c0018_action_log_message_id_text"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.add_column(
        "billing_usage",
        sa.Column("payer_user_id", postgresql.UUID(as_uuid=True), nullable=True),
    )
    op.execute("UPDATE billing_usage SET payer_user_id = user_id")
    op.alter_column("billing_usage", "payer_user_id", nullable=False)
    op.create_foreign_key(
        "fk_billing_usage_payer_user_id",
        "billing_usage",
        "user",
        ["payer_user_id"],
        ["id"],
        ondelete="CASCADE",
    )
    op.create_index(
        "ix_billing_usage_payer_created",
        "billing_usage",
        ["payer_user_id", "created_at"],
    )


def downgrade() -> None:
    op.drop_index("ix_billing_usage_payer_created", table_name="billing_usage")
    op.drop_constraint(
        "fk_billing_usage_payer_user_id", "billing_usage", type_="foreignkey"
    )
    op.drop_column("billing_usage", "payer_user_id")
