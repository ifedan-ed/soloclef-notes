"""Drop upstream's old purchase, auto-reload and incentive-task storage.

Soloclef Notes bills through ``app.billing`` (plans, packs, ``billing_*``
tables). Upstream SurfSense's older paths were removed from the code on
2026-09-25 (commit cf7c82c75): ``app/payments`` (Stripe credit and page
purchases), Stripe reconciliation, credit auto-reload and the "star us on
GitHub" incentive tasks. Nothing maps or reads what they stored, so the
owner asked for it to go:

- tables ``credit_purchases``, ``page_purchases``, ``user_incentive_tasks``
  (all empty on the live database when this was written);
- their enum types ``creditpurchasestatus``, ``pagepurchasestatus``,
  ``incentivetasktype``;
- the five ``user.auto_reload_*`` columns (all defaults on live).

Kept, because app.billing uses them: ``user.stripe_customer_id``,
``user.credit_micros_balance`` (and ``credit_micros_reserved``), the
``signup_credit_claims`` table, and ``billing_ledger`` with its ``incentive``
rows.

None of these are in the Zero publication (``user`` publishes only ``id`` and
``credit_micros_balance``), so zero-cache needs no reset.

The downgrade recreates the structure only; the data (there was none) is in
the root-only backup ``docker/backups/surfsense-<ts>-before-c0011-*`` taken
before this was applied.

Revision ID: c0011_drop_upstream_payments
Revises: c0010_workspace_sharing
"""

from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

from alembic import op

revision: str = "c0011_drop_upstream_payments"
down_revision: str | None = "c0010_workspace_sharing"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

TABLES = ("credit_purchases", "page_purchases", "user_incentive_tasks")
ENUMS = {
    "creditpurchasestatus": ("PENDING", "COMPLETED", "FAILED"),
    "pagepurchasestatus": ("PENDING", "COMPLETED", "FAILED"),
    "incentivetasktype": ("GITHUB_STAR", "REDDIT_FOLLOW", "DISCORD_JOIN"),
}
USER_COLUMNS = (
    "auto_reload_enabled",
    "auto_reload_threshold_micros",
    "auto_reload_amount_micros",
    "auto_reload_payment_method_id",
    "auto_reload_failed_at",
)


def upgrade() -> None:
    for table in TABLES:
        op.execute(f'DROP TABLE IF EXISTS "{table}"')
    for name in ENUMS:
        op.execute(f'DROP TYPE IF EXISTS "{name}"')
    for column in USER_COLUMNS:
        op.execute(f'ALTER TABLE "user" DROP COLUMN IF EXISTS "{column}"')


def _enum(name: str) -> postgresql.ENUM:
    return postgresql.ENUM(*ENUMS[name], name=name, create_type=False)


def downgrade() -> None:
    bind = op.get_bind()
    for name, values in ENUMS.items():
        postgresql.ENUM(*values, name=name).create(bind, checkfirst=True)

    op.add_column(
        "user",
        sa.Column(
            "auto_reload_enabled",
            sa.Boolean(),
            nullable=False,
            server_default=sa.text("false"),
        ),
    )
    op.add_column(
        "user",
        sa.Column("auto_reload_threshold_micros", sa.BigInteger(), nullable=True),
    )
    op.add_column(
        "user", sa.Column("auto_reload_amount_micros", sa.BigInteger(), nullable=True)
    )
    op.add_column(
        "user", sa.Column("auto_reload_payment_method_id", sa.String(), nullable=True)
    )
    op.add_column(
        "user",
        sa.Column("auto_reload_failed_at", sa.TIMESTAMP(timezone=True), nullable=True),
    )

    def user_fk() -> sa.ForeignKey:
        return sa.ForeignKey("user.id", ondelete="CASCADE")

    op.create_table(
        "credit_purchases",
        sa.Column("id", sa.UUID(), primary_key=True),
        sa.Column("user_id", sa.UUID(), user_fk(), nullable=False),
        sa.Column("stripe_checkout_session_id", sa.String(255), nullable=False),
        sa.Column("stripe_payment_intent_id", sa.String(255), nullable=True),
        sa.Column("quantity", sa.Integer(), nullable=False),
        sa.Column("credit_micros_granted", sa.BigInteger(), nullable=False),
        sa.Column("amount_total", sa.Integer(), nullable=True),
        sa.Column("currency", sa.String(10), nullable=True),
        sa.Column(
            "source",
            sa.String(20),
            nullable=False,
            server_default=sa.text("'checkout'"),
        ),
        sa.Column("status", _enum("creditpurchasestatus"), nullable=False),
        sa.Column("completed_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), nullable=False),
    )
    op.create_table(
        "page_purchases",
        sa.Column("id", sa.UUID(), primary_key=True),
        sa.Column("user_id", sa.UUID(), user_fk(), nullable=False),
        sa.Column("stripe_checkout_session_id", sa.String(255), nullable=False),
        sa.Column("stripe_payment_intent_id", sa.String(255), nullable=True),
        sa.Column("quantity", sa.Integer(), nullable=False),
        sa.Column("pages_granted", sa.Integer(), nullable=False),
        sa.Column("amount_total", sa.Integer(), nullable=True),
        sa.Column("currency", sa.String(10), nullable=True),
        sa.Column(
            "status",
            _enum("pagepurchasestatus"),
            nullable=False,
            server_default=sa.text("'PENDING'::pagepurchasestatus"),
        ),
        sa.Column("completed_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), nullable=False),
    )
    for table in ("credit_purchases", "page_purchases"):
        op.create_index(f"ix_{table}_created_at", table, ["created_at"])
        op.create_index(f"ix_{table}_status", table, ["status"])
        op.create_index(
            f"ix_{table}_stripe_checkout_session_id",
            table,
            ["stripe_checkout_session_id"],
            unique=True,
        )
        op.create_index(
            f"ix_{table}_stripe_payment_intent_id", table, ["stripe_payment_intent_id"]
        )
        op.create_index(f"ix_{table}_user_id", table, ["user_id"])

    op.create_table(
        "user_incentive_tasks",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("user_id", sa.UUID(), user_fk(), nullable=False),
        sa.Column("task_type", _enum("incentivetasktype"), nullable=False),
        sa.Column("credit_micros_awarded", sa.BigInteger(), nullable=False),
        sa.Column("completed_at", sa.TIMESTAMP(timezone=True), nullable=False),
        sa.Column("created_at", sa.TIMESTAMP(timezone=True), nullable=False),
        sa.UniqueConstraint("user_id", "task_type", name="uq_user_incentive_task"),
    )
    for column in ("created_at", "id", "task_type", "user_id"):
        op.create_index(
            f"ix_user_incentive_tasks_{column}", "user_incentive_tasks", [column]
        )
