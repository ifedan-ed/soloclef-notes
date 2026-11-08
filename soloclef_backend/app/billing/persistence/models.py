"""Billing tables: the catalog an admin edits, and the accounts and ledger it drives.

Catalog (admin-editable, seeded with defaults):
    billing_plans, billing_action_prices, billing_packs, billing_settings

Per person:
    billing_accounts   one row per user: plan, current period, plan credits left,
                       holds in flight, exemption
    billing_ledger     every movement of credit, append-only
    billing_usage      every metered action: what it cost (or would have cost in
                       shadow mode), and the hold behind it while it runs
    billing_payments   every Stripe checkout and crypto payment
    billing_webhook_events  provider event ids already processed (replay guard)

The top-up balance stays in ``user.credit_micros_balance``, where purchases,
the signup credit and incentives already landed; only ledger.py writes it now.
"""

from __future__ import annotations

from datetime import UTC, datetime

from sqlalchemy import (
    TIMESTAMP,
    BigInteger,
    Boolean,
    Column,
    ForeignKey,
    Index,
    Integer,
    String,
    Text,
    UniqueConstraint,
    text,
)
from sqlalchemy.dialects.postgresql import JSONB, UUID

from app.db import Base


def _now() -> datetime:
    return datetime.now(UTC)


class BillingPlan(Base):
    __tablename__ = "billing_plans"

    id = Column(Integer, primary_key=True)
    slug = Column(String(64), nullable=False, unique=True)
    sort_order = Column(Integer, nullable=False, default=0, server_default="0")
    active = Column(Boolean, nullable=False, default=True, server_default="true")
    # Shown on the pricing and billing pages; a hidden plan can still be
    # assigned by an admin.
    public = Column(Boolean, nullable=False, default=True, server_default="true")
    # The plan every account without a paid one is on (exactly one).
    is_default = Column(Boolean, nullable=False, default=False, server_default="false")
    price_cents = Column(Integer, nullable=False, default=0, server_default="0")
    currency = Column(String(8), nullable=False, default="usd", server_default="usd")
    monthly_credits_micros = Column(
        BigInteger, nullable=False, default=0, server_default="0"
    )
    entitlements = Column(JSONB, nullable=False, default=dict, server_default="{}")
    # {"en": {"name", "tagline", "features": [...]}, "es": {...}, ...}
    content = Column(JSONB, nullable=False, default=dict, server_default="{}")
    # Stripe Price used for new subscriptions; created on demand and replaced
    # when the price changes (existing subscribers keep theirs).
    stripe_price_id = Column(String(255), nullable=True)
    stripe_price_cents = Column(Integer, nullable=True)
    updated_at = Column(TIMESTAMP(timezone=True), nullable=False, default=_now)


class BillingActionPrice(Base):
    __tablename__ = "billing_action_prices"

    action = Column(String(64), primary_key=True)
    micros = Column(BigInteger, nullable=False, default=0, server_default="0")
    # What one unit is: "turn", "page", "image", "output", ...
    unit = Column(String(32), nullable=False, default="action", server_default="action")
    sort_order = Column(Integer, nullable=False, default=0, server_default="0")
    content = Column(JSONB, nullable=False, default=dict, server_default="{}")
    updated_at = Column(TIMESTAMP(timezone=True), nullable=False, default=_now)


class BillingPack(Base):
    __tablename__ = "billing_packs"

    id = Column(Integer, primary_key=True)
    slug = Column(String(64), nullable=False, unique=True)
    active = Column(Boolean, nullable=False, default=True, server_default="true")
    sort_order = Column(Integer, nullable=False, default=0, server_default="0")
    credits_micros = Column(BigInteger, nullable=False)
    price_cents = Column(Integer, nullable=False)
    currency = Column(String(8), nullable=False, default="usd", server_default="usd")
    content = Column(JSONB, nullable=False, default=dict, server_default="{}")
    updated_at = Column(TIMESTAMP(timezone=True), nullable=False, default=_now)


class BillingSetting(Base):
    __tablename__ = "billing_settings"

    key = Column(String(128), primary_key=True)
    # Plain settings hold their JSON value; secrets hold {"enc": "<fernet>"}
    # and are never returned by any API.
    value = Column(JSONB, nullable=True)
    secret = Column(Boolean, nullable=False, default=False, server_default="false")
    updated_at = Column(TIMESTAMP(timezone=True), nullable=False, default=_now)
    updated_by = Column(UUID(as_uuid=True), nullable=True)


class BillingAccount(Base):
    __tablename__ = "billing_accounts"

    user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="CASCADE"),
        primary_key=True,
    )
    plan_id = Column(Integer, ForeignKey("billing_plans.id"), nullable=False)
    # default | stripe | crypto | admin
    plan_source = Column(String(16), nullable=False, default="default")
    # active | past_due | canceled
    plan_status = Column(String(16), nullable=False, default="active")
    period_start = Column(TIMESTAMP(timezone=True), nullable=False, default=_now)
    period_end = Column(TIMESTAMP(timezone=True), nullable=False)
    # Prepaid (crypto) or admin-granted plans end here and fall back to the
    # default plan; ``None`` for the default plan and Stripe subscriptions.
    plan_paid_until = Column(TIMESTAMP(timezone=True), nullable=True)
    # Plan credits left this period. They do not roll over.
    plan_credits_micros = Column(BigInteger, nullable=False, default=0)
    # Sum of open holds (billing_usage rows in status "reserved", live mode).
    reserved_micros = Column(BigInteger, nullable=False, default=0)
    exempt = Column(Boolean, nullable=False, default=False, server_default="false")
    exempt_reason = Column(Text, nullable=True)
    stripe_subscription_id = Column(String(255), nullable=True, index=True)
    cancel_at_period_end = Column(
        Boolean, nullable=False, default=False, server_default="false"
    )
    created_at = Column(TIMESTAMP(timezone=True), nullable=False, default=_now)
    updated_at = Column(TIMESTAMP(timezone=True), nullable=False, default=_now)


class BillingLedgerEntry(Base):
    __tablename__ = "billing_ledger"
    __table_args__ = (
        # Idempotency: one grant per invoice, one debit per usage, ...
        Index(
            "uq_billing_ledger_kind_reference",
            "kind",
            "reference",
            unique=True,
            postgresql_where=text("reference IS NOT NULL"),
        ),
        Index("ix_billing_ledger_user_created", "user_id", "created_at"),
    )

    id = Column(BigInteger, primary_key=True)
    user_id = Column(
        UUID(as_uuid=True), ForeignKey("user.id", ondelete="CASCADE"), nullable=False
    )
    # plan | topup
    bucket = Column(String(8), nullable=False)
    delta_micros = Column(BigInteger, nullable=False)
    balance_after_micros = Column(BigInteger, nullable=False)
    # opening | plan_grant | plan_expire | plan_adjust | purchase | signup |
    # incentive | debit | refund | admin_adjust
    kind = Column(String(24), nullable=False)
    reason = Column(Text, nullable=True)
    reference = Column(String(255), nullable=True)
    usage_id = Column(BigInteger, nullable=True, index=True)
    actor_user_id = Column(UUID(as_uuid=True), nullable=True)
    created_at = Column(TIMESTAMP(timezone=True), nullable=False, default=_now)


class BillingUsage(Base):
    __tablename__ = "billing_usage"
    __table_args__ = (
        Index("ix_billing_usage_user_created", "user_id", "created_at"),
        Index(
            "uq_billing_usage_reference",
            "reference",
            unique=True,
            postgresql_where=text("reference IS NOT NULL"),
        ),
        Index(
            "ix_billing_usage_open_holds",
            "status",
            "created_at",
            postgresql_where=text("status = 'reserved'"),
        ),
    )

    id = Column(BigInteger, primary_key=True)
    # Who performed the action.
    user_id = Column(
        UUID(as_uuid=True), ForeignKey("user.id", ondelete="CASCADE"), nullable=False
    )
    # Who paid for it: the workspace owner for a workspace-scoped action
    # (c0019), else the same as ``user_id``. Fixed at ``begin``/``record``
    # time, so a membership change mid-job never moves an open hold.
    payer_user_id = Column(
        UUID(as_uuid=True), ForeignKey("user.id", ondelete="CASCADE"), nullable=False
    )
    workspace_id = Column(Integer, nullable=True)
    notebook_id = Column(Integer, nullable=True, index=True)
    thread_id = Column(Integer, nullable=True)
    action = Column(String(64), nullable=False, index=True)
    units = Column(Integer, nullable=False, default=1)
    unit_micros = Column(BigInteger, nullable=False, default=0)
    reserved_micros = Column(BigInteger, nullable=False, default=0)
    charged_micros = Column(BigInteger, nullable=False, default=0)
    # live | shadow | exempt
    mode = Column(String(8), nullable=False)
    # reserved | settled | released | refunded | denied
    status = Column(String(10), nullable=False)
    # In shadow mode: why live mode would have refused it.
    denial_code = Column(String(48), nullable=True)
    # Caller's idempotency key, e.g. "deliverable_job:123".
    reference = Column(String(255), nullable=True)
    details = Column(JSONB, nullable=False, default=dict, server_default="{}")
    created_at = Column(TIMESTAMP(timezone=True), nullable=False, default=_now)
    settled_at = Column(TIMESTAMP(timezone=True), nullable=True)


class BillingPayment(Base):
    __tablename__ = "billing_payments"
    __table_args__ = (
        UniqueConstraint("provider", "provider_ref", name="uq_billing_payments_ref"),
        Index("ix_billing_payments_user_created", "user_id", "created_at"),
    )

    id = Column(BigInteger, primary_key=True)
    user_id = Column(
        UUID(as_uuid=True), ForeignKey("user.id", ondelete="CASCADE"), nullable=False
    )
    # stripe | btcpay
    provider = Column(String(16), nullable=False)
    # pack | subscription | plan_months
    kind = Column(String(16), nullable=False)
    provider_ref = Column(String(255), nullable=False)
    # pending | paid | expired | invalid | partial | failed | refunded
    status = Column(String(12), nullable=False, default="pending")
    amount_cents = Column(Integer, nullable=False)
    currency = Column(String(8), nullable=False, default="usd")
    credits_micros = Column(BigInteger, nullable=False, default=0)
    plan_id = Column(Integer, nullable=True)
    months = Column(Integer, nullable=True)
    pack_id = Column(Integer, nullable=True)
    checkout_url = Column(Text, nullable=True)
    receipt_url = Column(Text, nullable=True)
    details = Column(JSONB, nullable=False, default=dict, server_default="{}")
    created_at = Column(TIMESTAMP(timezone=True), nullable=False, default=_now)
    completed_at = Column(TIMESTAMP(timezone=True), nullable=True)


class BillingWebhookEvent(Base):
    __tablename__ = "billing_webhook_events"

    provider = Column(String(16), primary_key=True)
    event_id = Column(String(255), primary_key=True)
    event_type = Column(String(128), nullable=True)
    received_at = Column(TIMESTAMP(timezone=True), nullable=False, default=_now)
