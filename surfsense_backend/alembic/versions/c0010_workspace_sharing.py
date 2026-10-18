"""Workspaces: email-bound invites, Owner-only public links that expire.

- ``workspace_invites.email`` / ``last_sent_at``: an invite may be bound to
  one registered account (then emailed and single-use); a plain link invite
  stays usable by any signed-in account.
- ``public_chat_snapshots``: ``expires_at``; ``workspace_id`` (backfilled from
  the thread) so a link can outlive the question of which chat it came from;
  ``notebook_output_id`` for a shared Studio output, with ``thread_id`` now
  nullable and exactly one of the two set.
- The Editor system role loses ``public_sharing:create``: only Owners create
  public links by default. Invites that grant a full-access role are switched
  off, since nobody may hand out ownership through an invite any more.
- Messaging channels (upstream's Telegram/WhatsApp/Slack/Discord gateway) are
  removed: ``external_chat_inbound_events``, ``external_chat_bindings``,
  ``external_chat_accounts`` and their enum types are dropped, with
  ``new_chat_threads.external_chat_binding_id``. All were empty on this
  deployment. The downgrade does not recreate them (see ``downgrade``).

Local revision, namespaced as custom/README.md explains.

Revision ID: c0010_workspace_sharing
Revises: c0009_billing_plans
"""

from collections.abc import Sequence

import sqlalchemy as sa

from alembic import op

revision: str = "c0010_workspace_sharing"
down_revision: str | None = "c0009_billing_plans"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.add_column(
        "workspace_invites", sa.Column("email", sa.String(length=320), nullable=True)
    )
    op.add_column(
        "workspace_invites",
        sa.Column("last_sent_at", sa.TIMESTAMP(timezone=True), nullable=True),
    )
    op.create_index(
        "ix_workspace_invites_email", "workspace_invites", ["email"], unique=False
    )
    op.execute(
        "UPDATE workspace_invites i SET is_active = false "
        "FROM workspace_roles r "
        "WHERE i.role_id = r.id AND '*' = ANY(r.permissions) AND i.is_active"
    )

    op.add_column(
        "public_chat_snapshots",
        sa.Column("expires_at", sa.TIMESTAMP(timezone=True), nullable=True),
    )
    op.add_column(
        "public_chat_snapshots",
        sa.Column("workspace_id", sa.Integer(), nullable=True),
    )
    op.add_column(
        "public_chat_snapshots",
        sa.Column("notebook_output_id", sa.Integer(), nullable=True),
    )
    op.execute(
        "UPDATE public_chat_snapshots s SET workspace_id = t.workspace_id "
        "FROM new_chat_threads t WHERE s.thread_id = t.id"
    )
    op.execute("DELETE FROM public_chat_snapshots WHERE workspace_id IS NULL")
    op.alter_column("public_chat_snapshots", "workspace_id", nullable=False)
    op.alter_column("public_chat_snapshots", "thread_id", nullable=True)
    op.create_foreign_key(
        "fk_public_chat_snapshots_workspace_id",
        "public_chat_snapshots",
        "workspaces",
        ["workspace_id"],
        ["id"],
        ondelete="CASCADE",
    )
    op.create_foreign_key(
        "fk_public_chat_snapshots_notebook_output_id",
        "public_chat_snapshots",
        "notebook_outputs",
        ["notebook_output_id"],
        ["id"],
        ondelete="CASCADE",
    )
    op.create_index(
        "ix_public_chat_snapshots_workspace_id",
        "public_chat_snapshots",
        ["workspace_id"],
    )
    op.create_index(
        "ix_public_chat_snapshots_notebook_output_id",
        "public_chat_snapshots",
        ["notebook_output_id"],
    )
    op.create_check_constraint(
        "ck_public_snapshot_one_subject",
        "public_chat_snapshots",
        "num_nonnulls(thread_id, notebook_output_id) = 1",
    )

    op.execute(
        "UPDATE workspace_roles "
        "SET permissions = array_remove(permissions, 'public_sharing:create') "
        "WHERE is_system_role AND name = 'Editor'"
    )

    # Messaging channels: the column first (its FK and index go with it), then
    # the tables child-first, then the enum types they used.
    op.execute(
        "ALTER TABLE new_chat_threads DROP COLUMN IF EXISTS external_chat_binding_id"
    )
    for table in _GATEWAY_TABLES:
        op.execute(f"DROP TABLE IF EXISTS {table} CASCADE")
    for enum_type in _GATEWAY_ENUMS:
        op.execute(f"DROP TYPE IF EXISTS {enum_type}")


_GATEWAY_TABLES = (
    "external_chat_inbound_events",
    "external_chat_bindings",
    "external_chat_accounts",
)
_GATEWAY_ENUMS = (
    "external_chat_event_status",
    "external_chat_event_kind",
    "external_chat_peer_kind",
    "external_chat_binding_state",
    "external_chat_health_status",
    "external_chat_account_mode",
    "external_chat_platform",
)


def downgrade() -> None:
    # The messaging-channel tables are not recreated: the code that used them
    # is gone, and they held no rows. Revision 149 has their full shape if a
    # restore is ever needed.
    op.execute(
        "UPDATE workspace_roles "
        "SET permissions = array_append(permissions, 'public_sharing:create') "
        "WHERE is_system_role AND name = 'Editor' "
        "AND NOT ('public_sharing:create' = ANY(permissions))"
    )
    op.drop_constraint(
        "ck_public_snapshot_one_subject", "public_chat_snapshots", type_="check"
    )
    op.execute("DELETE FROM public_chat_snapshots WHERE thread_id IS NULL")
    op.drop_index(
        "ix_public_chat_snapshots_notebook_output_id",
        table_name="public_chat_snapshots",
    )
    op.drop_index(
        "ix_public_chat_snapshots_workspace_id", table_name="public_chat_snapshots"
    )
    op.drop_constraint(
        "fk_public_chat_snapshots_notebook_output_id",
        "public_chat_snapshots",
        type_="foreignkey",
    )
    op.drop_constraint(
        "fk_public_chat_snapshots_workspace_id",
        "public_chat_snapshots",
        type_="foreignkey",
    )
    op.alter_column("public_chat_snapshots", "thread_id", nullable=False)
    op.drop_column("public_chat_snapshots", "notebook_output_id")
    op.drop_column("public_chat_snapshots", "workspace_id")
    op.drop_column("public_chat_snapshots", "expires_at")
    op.drop_index("ix_workspace_invites_email", table_name="workspace_invites")
    op.drop_column("workspace_invites", "last_sent_at")
    op.drop_column("workspace_invites", "email")
