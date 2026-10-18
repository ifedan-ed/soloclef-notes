"""Billing: plans, per-action prices, packs, accounts, ledger, usage, payments.

Seeds the owner-approved defaults (Free / Plus / Pro, the per-action credit
prices and two top-up packs) as ordinary rows an admin edits afterwards, and
opens an account for every existing user on the default plan. Accounts of
superusers, and of the addresses in ``BILLING_ADMIN_EMAILS`` when the variable
is set where the migration runs, start exempt. Each user's existing wallet
balance becomes an ``opening`` ledger entry so the ledger sums to it.

``user.credit_micros_reserved`` is zeroed: holds now live in
``billing_accounts.reserved_micros``.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0009_billing_plans
Revises: c0008_notebook_title_source
"""

import json
import os
from collections.abc import Sequence

import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

from alembic import op

revision: str = "c0009_billing_plans"
down_revision: str | None = "c0008_notebook_title_source"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

# A frozen copy of app.billing.defaults at the time of this migration.
_STUDIO = [
    "quiz",
    "flashcards",
    "mindmap",
    "slides",
    "data_table",
    "infographic",
    "audio",
    "report",
    "video",
]
_C = 10_000  # micros per credit


def _ent(parsers, vision, tiers, kinds, video, pages, jobs):
    return {
        "parsers": parsers,
        "vision": vision,
        "model_tiers": tiers,
        "studio_kinds": kinds,
        "max_video_seconds": video,
        "max_pages_per_upload": pages,
        "max_concurrent_jobs": jobs,
        "max_notebooks": 0,
        "max_sources_per_notebook": 0,
    }


_PLANS = [
    (
        "free",
        0,
        True,
        0,
        100,
        _ent(
            ["local"],
            False,
            ["standard"],
            [k for k in _STUDIO if k != "video"],
            0,
            50,
            1,
        ),
        {
            "name": "Free",
            "tagline": "Try notebooks with your own documents.",
            "features": [
                "100 credits every month",
                "Local document parsing",
                "Standard models",
                "Every Studio output except video",
            ],
        },
    ),
    (
        "plus",
        1,
        False,
        900,
        1500,
        _ent(["local", "cloud"], True, ["standard", "premium"], _STUDIO, 300, 300, 2),
        {
            "name": "Plus",
            "tagline": "For regular study and research.",
            "features": [
                "1,500 credits every month",
                "Cloud parsing for scanned and complex files",
                "Image understanding",
                "Premium models",
                "All Studio outputs",
            ],
        },
    ),
    (
        "pro",
        2,
        False,
        2400,
        5000,
        _ent(["local", "cloud"], True, ["standard", "premium"], _STUDIO, 600, 1000, 4),
        {
            "name": "Pro",
            "tagline": "For heavy use and long videos.",
            "features": [
                "5,000 credits every month",
                "Everything in Plus",
                "Videos up to 10 minutes",
                "Larger uploads and more jobs at once",
            ],
        },
    ),
]

_PRICES = [
    ("chat_standard", 1, "turn", "Chat message (standard model)"),
    ("chat_premium", 5, "turn", "Chat message (premium model)"),
    ("cloud_page", 2, "page", "Cloud parsing"),
    ("local_page", 0, "page", "Local parsing"),
    ("vision_image", 1, "image", "Image understanding (vision)"),
    ("image_generation", 4, "image", "Image generation"),
    ("studio_quiz", 5, "output", "Quiz"),
    ("studio_flashcards", 5, "output", "Flashcards"),
    ("studio_mindmap", 10, "output", "Mind map"),
    ("studio_slides", 10, "output", "Slides"),
    ("studio_data_table", 10, "output", "Data table"),
    ("studio_infographic", 15, "output", "Infographic"),
    ("studio_audio", 15, "output", "Audio overview"),
    ("studio_report", 20, "output", "Report"),
    ("studio_video", 40, "output", "Video overview"),
    ("embedding", 0, "document", "Embeddings (search index)"),
]

_PACKS = [
    ("credits-500", 0, 500, 500, "500 credits"),
    ("credits-1200", 1, 1200, 1000, "1,200 credits"),
]


def upgrade() -> None:
    uuid = postgresql.UUID(as_uuid=True)
    jsonb = postgresql.JSONB
    ts = sa.TIMESTAMP(timezone=True)
    now = sa.text("now()")

    op.create_table(
        "billing_plans",
        sa.Column("id", sa.Integer, primary_key=True),
        sa.Column("slug", sa.String(64), nullable=False, unique=True),
        sa.Column("sort_order", sa.Integer, nullable=False, server_default="0"),
        sa.Column("active", sa.Boolean, nullable=False, server_default="true"),
        sa.Column("public", sa.Boolean, nullable=False, server_default="true"),
        sa.Column("is_default", sa.Boolean, nullable=False, server_default="false"),
        sa.Column("price_cents", sa.Integer, nullable=False, server_default="0"),
        sa.Column("currency", sa.String(8), nullable=False, server_default="usd"),
        sa.Column(
            "monthly_credits_micros", sa.BigInteger, nullable=False, server_default="0"
        ),
        sa.Column("entitlements", jsonb, nullable=False, server_default="{}"),
        sa.Column("content", jsonb, nullable=False, server_default="{}"),
        sa.Column("stripe_price_id", sa.String(255), nullable=True),
        sa.Column("stripe_price_cents", sa.Integer, nullable=True),
        sa.Column("updated_at", ts, nullable=False, server_default=now),
    )
    op.create_table(
        "billing_action_prices",
        sa.Column("action", sa.String(64), primary_key=True),
        sa.Column("micros", sa.BigInteger, nullable=False, server_default="0"),
        sa.Column("unit", sa.String(32), nullable=False, server_default="action"),
        sa.Column("sort_order", sa.Integer, nullable=False, server_default="0"),
        sa.Column("content", jsonb, nullable=False, server_default="{}"),
        sa.Column("updated_at", ts, nullable=False, server_default=now),
    )
    op.create_table(
        "billing_packs",
        sa.Column("id", sa.Integer, primary_key=True),
        sa.Column("slug", sa.String(64), nullable=False, unique=True),
        sa.Column("active", sa.Boolean, nullable=False, server_default="true"),
        sa.Column("sort_order", sa.Integer, nullable=False, server_default="0"),
        sa.Column("credits_micros", sa.BigInteger, nullable=False),
        sa.Column("price_cents", sa.Integer, nullable=False),
        sa.Column("currency", sa.String(8), nullable=False, server_default="usd"),
        sa.Column("content", jsonb, nullable=False, server_default="{}"),
        sa.Column("updated_at", ts, nullable=False, server_default=now),
    )
    op.create_table(
        "billing_settings",
        sa.Column("key", sa.String(128), primary_key=True),
        sa.Column("value", jsonb, nullable=True),
        sa.Column("secret", sa.Boolean, nullable=False, server_default="false"),
        sa.Column("updated_at", ts, nullable=False, server_default=now),
        sa.Column("updated_by", uuid, nullable=True),
    )
    op.create_table(
        "billing_accounts",
        sa.Column(
            "user_id",
            uuid,
            sa.ForeignKey("user.id", ondelete="CASCADE"),
            primary_key=True,
        ),
        sa.Column(
            "plan_id", sa.Integer, sa.ForeignKey("billing_plans.id"), nullable=False
        ),
        sa.Column(
            "plan_source", sa.String(16), nullable=False, server_default="default"
        ),
        sa.Column(
            "plan_status", sa.String(16), nullable=False, server_default="active"
        ),
        sa.Column("period_start", ts, nullable=False, server_default=now),
        sa.Column("period_end", ts, nullable=False),
        sa.Column("plan_paid_until", ts, nullable=True),
        sa.Column(
            "plan_credits_micros", sa.BigInteger, nullable=False, server_default="0"
        ),
        sa.Column("reserved_micros", sa.BigInteger, nullable=False, server_default="0"),
        sa.Column("exempt", sa.Boolean, nullable=False, server_default="false"),
        sa.Column("exempt_reason", sa.Text, nullable=True),
        sa.Column("stripe_subscription_id", sa.String(255), nullable=True),
        sa.Column(
            "cancel_at_period_end", sa.Boolean, nullable=False, server_default="false"
        ),
        sa.Column("created_at", ts, nullable=False, server_default=now),
        sa.Column("updated_at", ts, nullable=False, server_default=now),
    )
    op.create_index(
        "ix_billing_accounts_stripe_subscription_id",
        "billing_accounts",
        ["stripe_subscription_id"],
    )
    op.create_table(
        "billing_ledger",
        sa.Column("id", sa.BigInteger, primary_key=True),
        sa.Column(
            "user_id",
            uuid,
            sa.ForeignKey("user.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("bucket", sa.String(8), nullable=False),
        sa.Column("delta_micros", sa.BigInteger, nullable=False),
        sa.Column("balance_after_micros", sa.BigInteger, nullable=False),
        sa.Column("kind", sa.String(24), nullable=False),
        sa.Column("reason", sa.Text, nullable=True),
        sa.Column("reference", sa.String(255), nullable=True),
        sa.Column("usage_id", sa.BigInteger, nullable=True),
        sa.Column("actor_user_id", uuid, nullable=True),
        sa.Column("created_at", ts, nullable=False, server_default=now),
    )
    op.create_index(
        "uq_billing_ledger_kind_reference",
        "billing_ledger",
        ["kind", "reference"],
        unique=True,
        postgresql_where=sa.text("reference IS NOT NULL"),
    )
    op.create_index(
        "ix_billing_ledger_user_created", "billing_ledger", ["user_id", "created_at"]
    )
    op.create_index("ix_billing_ledger_usage_id", "billing_ledger", ["usage_id"])
    op.create_table(
        "billing_usage",
        sa.Column("id", sa.BigInteger, primary_key=True),
        sa.Column(
            "user_id",
            uuid,
            sa.ForeignKey("user.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("workspace_id", sa.Integer, nullable=True),
        sa.Column("notebook_id", sa.Integer, nullable=True),
        sa.Column("thread_id", sa.Integer, nullable=True),
        sa.Column("action", sa.String(64), nullable=False),
        sa.Column("units", sa.Integer, nullable=False, server_default="1"),
        sa.Column("unit_micros", sa.BigInteger, nullable=False, server_default="0"),
        sa.Column("reserved_micros", sa.BigInteger, nullable=False, server_default="0"),
        sa.Column("charged_micros", sa.BigInteger, nullable=False, server_default="0"),
        sa.Column("mode", sa.String(8), nullable=False),
        sa.Column("status", sa.String(10), nullable=False),
        sa.Column("denial_code", sa.String(48), nullable=True),
        sa.Column("reference", sa.String(255), nullable=True),
        sa.Column("details", jsonb, nullable=False, server_default="{}"),
        sa.Column("created_at", ts, nullable=False, server_default=now),
        sa.Column("settled_at", ts, nullable=True),
    )
    op.create_index(
        "ix_billing_usage_user_created", "billing_usage", ["user_id", "created_at"]
    )
    op.create_index("ix_billing_usage_action", "billing_usage", ["action"])
    op.create_index("ix_billing_usage_notebook_id", "billing_usage", ["notebook_id"])
    op.create_index(
        "uq_billing_usage_reference",
        "billing_usage",
        ["reference"],
        unique=True,
        postgresql_where=sa.text("reference IS NOT NULL"),
    )
    op.create_index(
        "ix_billing_usage_open_holds",
        "billing_usage",
        ["status", "created_at"],
        postgresql_where=sa.text("status = 'reserved'"),
    )
    op.create_table(
        "billing_payments",
        sa.Column("id", sa.BigInteger, primary_key=True),
        sa.Column(
            "user_id",
            uuid,
            sa.ForeignKey("user.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("provider", sa.String(16), nullable=False),
        sa.Column("kind", sa.String(16), nullable=False),
        sa.Column("provider_ref", sa.String(255), nullable=False),
        sa.Column("status", sa.String(12), nullable=False, server_default="pending"),
        sa.Column("amount_cents", sa.Integer, nullable=False),
        sa.Column("currency", sa.String(8), nullable=False, server_default="usd"),
        sa.Column("credits_micros", sa.BigInteger, nullable=False, server_default="0"),
        sa.Column("plan_id", sa.Integer, nullable=True),
        sa.Column("months", sa.Integer, nullable=True),
        sa.Column("pack_id", sa.Integer, nullable=True),
        sa.Column("checkout_url", sa.Text, nullable=True),
        sa.Column("receipt_url", sa.Text, nullable=True),
        sa.Column("details", jsonb, nullable=False, server_default="{}"),
        sa.Column("created_at", ts, nullable=False, server_default=now),
        sa.Column("completed_at", ts, nullable=True),
        sa.UniqueConstraint("provider", "provider_ref", name="uq_billing_payments_ref"),
    )
    op.create_index(
        "ix_billing_payments_user_created",
        "billing_payments",
        ["user_id", "created_at"],
    )
    op.create_table(
        "billing_webhook_events",
        sa.Column("provider", sa.String(16), primary_key=True),
        sa.Column("event_id", sa.String(255), primary_key=True),
        sa.Column("event_type", sa.String(128), nullable=True),
        sa.Column("received_at", ts, nullable=False, server_default=now),
    )

    bind = op.get_bind()
    for slug, order, is_default, cents, credits, ent, content in _PLANS:
        bind.execute(
            sa.text(
                "INSERT INTO billing_plans (slug, sort_order, is_default, price_cents,"
                " monthly_credits_micros, entitlements, content) VALUES (:slug, :o, :d,"
                " :c, :m, CAST(:e AS jsonb), CAST(:t AS jsonb))"
            ),
            {
                "slug": slug,
                "o": order,
                "d": is_default,
                "c": cents,
                "m": credits * _C,
                "e": json.dumps(ent),
                "t": json.dumps({"en": content}),
            },
        )
    for order, (action, credits, unit, label) in enumerate(_PRICES):
        bind.execute(
            sa.text(
                "INSERT INTO billing_action_prices (action, micros, unit, sort_order,"
                " content) VALUES (:a, :m, :u, :o, CAST(:t AS jsonb))"
            ),
            {
                "a": action,
                "m": credits * _C,
                "u": unit,
                "o": order,
                "t": json.dumps({"en": {"label": label}}),
            },
        )
    for slug, order, credits, cents, name in _PACKS:
        bind.execute(
            sa.text(
                "INSERT INTO billing_packs (slug, sort_order, credits_micros, price_cents,"
                " content) VALUES (:s, :o, :m, :c, CAST(:t AS jsonb))"
            ),
            {
                "s": slug,
                "o": order,
                "m": credits * _C,
                "c": cents,
                "t": json.dumps({"en": {"name": name}}),
            },
        )

    admins = [
        e.strip().lower()
        for e in os.getenv("BILLING_ADMIN_EMAILS", "").split(",")
        if e.strip()
    ]
    # Every existing user: an account on the default plan with this month's
    # allowance, and an opening entry for the wallet balance they already have.
    bind.execute(
        sa.text(
            """
            INSERT INTO billing_accounts (user_id, plan_id, plan_source, period_start,
                period_end, plan_credits_micros, exempt, exempt_reason)
            SELECT u.id, p.id, 'default', now(), now() + interval '1 month',
                p.monthly_credits_micros,
                (u.is_superuser OR lower(u.email) = ANY(:admins)),
                CASE WHEN (u.is_superuser OR lower(u.email) = ANY(:admins))
                     THEN 'Administrator' END
            FROM "user" u CROSS JOIN billing_plans p WHERE p.is_default
            """
        ),
        {"admins": admins},
    )
    bind.execute(
        sa.text(
            """
            INSERT INTO billing_ledger (user_id, bucket, delta_micros,
                balance_after_micros, kind, reason, reference)
            SELECT a.user_id, 'plan', a.plan_credits_micros, a.plan_credits_micros,
                'plan_grant', 'First period',
                'grant:' || a.user_id || ':' || 'migration'
            FROM billing_accounts a
            """
        )
    )
    bind.execute(
        sa.text(
            """
            INSERT INTO billing_ledger (user_id, bucket, delta_micros,
                balance_after_micros, kind, reason, reference)
            SELECT u.id, 'topup', u.credit_micros_balance, u.credit_micros_balance,
                'opening', 'Balance before the ledger', 'opening:' || u.id
            FROM "user" u WHERE u.credit_micros_balance <> 0
            """
        )
    )
    bind.execute(sa.text('UPDATE "user" SET credit_micros_reserved = 0'))


def downgrade() -> None:
    for table in (
        "billing_webhook_events",
        "billing_payments",
        "billing_usage",
        "billing_ledger",
        "billing_accounts",
        "billing_settings",
        "billing_packs",
        "billing_action_prices",
        "billing_plans",
    ):
        op.drop_table(table)
