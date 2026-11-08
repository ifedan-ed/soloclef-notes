"""Apply a crypto provider's news to a payment, the same for any provider.

* The payment row is locked; a row already ``paid`` never changes again, so a
  replayed or late callback cannot grant twice.
* When the provider reports amounts, they decide, not only its word: less
  than the price minus the tolerance is ``partial`` (nothing is granted), and
  more is still ``paid`` with the surplus credited as top-up at 1 USD = 100
  credits. BTCPay applies its own store tolerance and reports no amounts, so
  its status decides; a partial invoice stays payable on its checkout page.
* A pack grants its credits; a plan purchase starts or extends a prepaid plan.
"""

from __future__ import annotations

import logging
from datetime import UTC, datetime
from decimal import Decimal

from dateutil.relativedelta import relativedelta
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from .. import ledger
from ..persistence.models import BillingPayment, BillingPlan
from ..units import MICROS_PER_DOLLAR
from . import provider as p

logger = logging.getLogger(__name__)


async def payment_for(
    session: AsyncSession, provider: str, ref: str
) -> BillingPayment | None:
    return await session.scalar(
        select(BillingPayment)
        .where(BillingPayment.provider == provider, BillingPayment.provider_ref == ref)
        .with_for_update()
    )


def _amount_status(
    status: str, expected: Decimal | None, paid: Decimal | None, tolerance_pct: float
) -> str:
    if status not in {p.PAID, p.PARTIAL} or not expected or paid is None:
        return status
    floor = expected * (Decimal(1) - Decimal(str(tolerance_pct)) / Decimal(100))
    return p.PAID if paid >= floor else p.PARTIAL


async def extend_prepaid_plan(
    session: AsyncSession, payment: BillingPayment, plan: BillingPlan
) -> None:
    months = max(1, int(payment.months or 1))
    account, user = await ledger.lock_account(session, payment.user_id)
    now = datetime.now(UTC)
    same = (
        account.plan_id == plan.id
        and account.plan_source == "crypto"
        and account.plan_paid_until is not None
        and account.plan_paid_until > now
    )
    if same:
        account.plan_paid_until = account.plan_paid_until + relativedelta(months=months)
        return
    await ledger.change_plan(
        session,
        account,
        user,
        plan,
        source="crypto",
        paid_until=now + relativedelta(months=months),
        reason=f"Prepaid {months} month(s) by crypto",
    )


async def apply_update(
    session: AsyncSession,
    provider: p.CryptoProvider,
    update: p.PaymentUpdate,
    *,
    tolerance_pct: float = 0,
) -> BillingPayment | None:
    payment = await payment_for(session, provider.name, update.provider_ref)
    if payment is None:
        logger.warning("%s payment %s is unknown", provider.name, update.provider_ref)
        return None
    if update.order_id and update.order_id != (payment.details or {}).get("order_id"):
        logger.warning(
            "%s payment %s: order id mismatch", provider.name, update.provider_ref
        )
        return None
    if payment.status == "paid":
        return payment
    expected = (
        Decimal(str((payment.details or {}).get("pay_amount") or 0))
        or update.pay_amount
    )
    status = _amount_status(
        provider.status(update),
        expected,
        update.actually_paid,
        tolerance_pct,
    )
    details = dict(payment.details or {})
    details.update(
        {
            "raw_status": update.raw_status,
            "actually_paid": str(update.actually_paid)
            if update.actually_paid is not None
            else None,
        }
    )
    if status == p.PARTIAL and expected and update.actually_paid is not None:
        details["remaining"] = str(max(Decimal(0), expected - update.actually_paid))
    if update.extra.get("over_paid"):
        details["overpaid"] = True
    payment.details = details

    if status != p.PAID:
        payment.status = {
            p.PENDING: "pending",
            p.CONFIRMING: "pending",
            p.PARTIAL: "partial",
            p.FAILED: "failed",
            p.EXPIRED: "expired",
            p.REFUNDED: "refunded",
        }.get(status, "pending")
        if payment.status in {"failed", "expired", "refunded"}:
            payment.completed_at = datetime.now(UTC)
        return payment

    reference = f"crypto:{provider.name}:{payment.provider_ref}"
    if payment.kind == "pack":
        await ledger.grant_topup(
            session,
            payment.user_id,
            int(payment.credits_micros),
            kind="purchase",
            reason="Credit pack (crypto)",
            reference=reference,
        )
    elif payment.kind == "plan_months":
        plan = await session.get(BillingPlan, payment.plan_id)
        if plan is None:
            logger.error("crypto payment %s names a missing plan", payment.id)
            return payment
        await extend_prepaid_plan(session, payment, plan)
    # Overpayment: the surplus, in USD at 1:1, becomes top-up credit.
    if (
        expected
        and update.actually_paid is not None
        and update.actually_paid > expected
    ):
        surplus_usd = (
            (update.actually_paid - expected)
            / expected
            * Decimal(payment.amount_cents)
            / 100
        )
        surplus = int(surplus_usd * MICROS_PER_DOLLAR)
        if surplus > 0:
            await ledger.grant_topup(
                session,
                payment.user_id,
                surplus,
                kind="purchase",
                reason="Crypto overpayment",
                reference=f"{reference}:surplus",
            )
            details["overpaid_usd"] = str(round(surplus_usd, 2))
            payment.details = details
    payment.status = "paid"
    payment.completed_at = datetime.now(UTC)
    return payment
