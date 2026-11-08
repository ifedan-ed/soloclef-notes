"""What each Stripe event does to accounts and payments.

Every handler is idempotent on its own (ledger references name the Stripe
object), and the webhook additionally records each event id once, so a
replayed or retried event changes nothing twice.

* ``checkout.session.completed`` / ``async_payment_succeeded`` — a paid pack
  grants its credits (``stripe_checkout:<session>``); a subscription checkout
  links the subscription to the account.
* ``invoice.paid`` for a new or renewed subscription — starts the plan's
  period and grants its monthly credits (``stripe_invoice:<invoice>``).
* ``customer.subscription.updated`` — plan changes (prorated credit
  difference), ``cancel_at_period_end``, past-due status.
* ``customer.subscription.deleted`` — back to the default plan.
* ``invoice.payment_failed`` — the account is marked past due.
* ``checkout.session.expired`` / ``async_payment_failed`` — the payment row
  is closed; nothing is granted.
"""

from __future__ import annotations

import logging
from datetime import UTC, datetime
from typing import Any
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import User

from .. import ledger
from ..persistence.models import (
    BillingAccount,
    BillingLedgerEntry,
    BillingPayment,
    BillingPlan,
)
from .flows import PURCHASE_PACK

logger = logging.getLogger(__name__)

PAID_STATUSES = frozenset({"paid", "no_payment_required"})


def _get(obj: Any, key: str, default: Any = None) -> Any:
    if obj is None:
        return default
    if isinstance(obj, dict):
        return obj.get(key, default)
    try:
        return obj[key]
    except (KeyError, TypeError, AttributeError):
        return getattr(obj, key, default)


def _id(value: Any) -> str | None:
    if value is None:
        return None
    if isinstance(value, str):
        return value
    return _get(value, "id")


def _metadata(obj: Any) -> dict[str, str]:
    meta = _get(obj, "metadata") or {}
    if hasattr(meta, "to_dict"):
        meta = meta.to_dict()
    return {str(k): str(v) for k, v in dict(meta).items()}


def _ts(value: Any) -> datetime | None:
    return datetime.fromtimestamp(int(value), UTC) if value else None


async def _payment(session: AsyncSession, ref: str) -> BillingPayment | None:
    return await session.scalar(
        select(BillingPayment)
        .where(BillingPayment.provider == "stripe", BillingPayment.provider_ref == ref)
        .with_for_update()
    )


async def _user_for(session: AsyncSession, obj: Any) -> UUID | None:
    meta = _metadata(obj)
    if meta.get("user_id"):
        try:
            return UUID(meta["user_id"])
        except ValueError:
            return None
    customer = _id(_get(obj, "customer"))
    if customer:
        return await session.scalar(
            select(User.id).where(User.stripe_customer_id == customer)
        )
    return None


# -- packs --------------------------------------------------------------------


async def fulfil_checkout(session: AsyncSession, checkout: Any) -> None:
    """A completed Checkout Session: grant a paid pack, or link a subscription."""
    ref = str(_id(checkout))
    meta = _metadata(checkout)
    payment = await _payment(session, ref)
    if _get(checkout, "mode") == "subscription":
        user_id = await _user_for(session, checkout)
        subscription = _id(_get(checkout, "subscription"))
        if user_id and subscription:
            account, _user = await ledger.lock_account(session, user_id)
            account.stripe_subscription_id = subscription
            if meta.get("plan_id") and account.plan_source != "stripe":
                # The period and its credits start with the first invoice.
                account.plan_source = "stripe"
        if payment is not None and payment.status == "pending":
            payment.status = "paid"
            payment.completed_at = datetime.now(UTC)
        return
    if meta.get("purchase_type") != PURCHASE_PACK:
        return
    if _get(checkout, "payment_status") not in PAID_STATUSES:
        return
    if payment is None:
        logger.warning("Paid Stripe checkout %s has no payment row; ignoring", ref)
        return
    if payment.status == "paid":
        return
    # The amount granted is the one recorded when the checkout was created
    # from the pack row, never anything the client or Stripe metadata says.
    await ledger.grant_topup(
        session,
        payment.user_id,
        int(payment.credits_micros),
        kind="purchase",
        reason="Credit pack (card)",
        reference=f"stripe_checkout:{ref}",
    )
    payment.status = "paid"
    payment.completed_at = datetime.now(UTC)
    invoice = _id(_get(checkout, "invoice"))
    if invoice:
        payment.details = {**(payment.details or {}), "invoice": invoice}


async def close_checkout(session: AsyncSession, checkout: Any, status: str) -> None:
    payment = await _payment(session, str(_id(checkout)))
    if payment is not None and payment.status == "pending":
        payment.status = status
        payment.completed_at = datetime.now(UTC)


# -- subscriptions ------------------------------------------------------------


def _invoice_subscription(invoice: Any) -> str | None:
    sub = _id(_get(invoice, "subscription"))
    if sub:
        return sub
    parent = _get(invoice, "parent") or {}
    details = _get(parent, "subscription_details") or {}
    return _id(_get(details, "subscription"))


def _invoice_line(invoice: Any) -> Any:
    lines = _get(_get(invoice, "lines") or {}, "data") or []
    return lines[0] if lines else None


def _line_price(line: Any) -> str | None:
    price = _id(_get(line, "price"))
    if price:
        return price
    pricing = _get(line, "pricing") or {}
    details = _get(pricing, "price_details") or {}
    return _id(_get(details, "price"))


async def _plan_for(
    session: AsyncSession, price_id: str | None, meta: dict[str, str]
) -> BillingPlan | None:
    if price_id:
        plan = await session.scalar(
            select(BillingPlan).where(BillingPlan.stripe_price_id == price_id)
        )
        if plan is not None:
            return plan
    if meta.get("plan_id", "").isdigit():
        return await session.get(BillingPlan, int(meta["plan_id"]))
    return None


async def invoice_paid(session: AsyncSession, invoice: Any, client=None) -> None:
    """A subscription's first or renewal invoice: start the period, grant credits."""
    if _get(invoice, "billing_reason") not in {
        "subscription_create",
        "subscription_cycle",
    }:
        return
    subscription_id = _invoice_subscription(invoice)
    if not subscription_id:
        return
    line = _invoice_line(invoice)
    meta = _metadata(line) if line is not None else {}
    subscription = None
    if client is not None and (not meta.get("user_id") or not meta.get("plan_id")):
        subscription = client.v1.subscriptions.retrieve(subscription_id)
        meta = {**_metadata(subscription), **meta}
    user_id = None
    if meta.get("user_id"):
        user_id = UUID(meta["user_id"])
    else:
        user_id = await _user_for(session, invoice)
    plan = await _plan_for(session, _line_price(line), meta)
    if user_id is None or plan is None:
        logger.warning("Stripe invoice %s: no user or plan; ignoring", _id(invoice))
        return
    period = _get(line, "period") or {}
    start = _ts(_get(period, "start")) or datetime.now(UTC)
    end = _ts(_get(period, "end"))
    reference = f"stripe_invoice:{_id(invoice)}"
    account, user = await ledger.lock_account(session, user_id, roll=False)
    seen = await session.scalar(
        select(BillingLedgerEntry.id).where(
            BillingLedgerEntry.kind == "plan_grant",
            BillingLedgerEntry.reference == reference,
        )
    )
    if seen is not None:
        return
    if account.plan_credits_micros:
        await ledger.post(
            session,
            account,
            user,
            bucket=ledger.PLAN,
            delta=-int(account.plan_credits_micros),
            kind="plan_expire",
            reason="Subscription period ended",
            reference=f"expire:{reference}",
        )
    account.plan_id = plan.id
    account.plan_source = "stripe"
    account.plan_status = "active"
    account.plan_paid_until = None
    account.stripe_subscription_id = subscription_id
    account.period_start = start
    if end is not None:
        account.period_end = end
    await ledger.post(
        session,
        account,
        user,
        bucket=ledger.PLAN,
        delta=int(plan.monthly_credits_micros),
        kind="plan_grant",
        reason="Subscription period",
        reference=reference,
    )
    payment = await _payment(session, str(_id(invoice)))
    if payment is None:
        session.add(
            BillingPayment(
                user_id=user_id,
                provider="stripe",
                kind="subscription",
                provider_ref=str(_id(invoice)),
                status="paid",
                amount_cents=int(_get(invoice, "amount_paid") or 0),
                currency=str(_get(invoice, "currency") or "usd"),
                plan_id=plan.id,
                months=1,
                receipt_url=_get(invoice, "hosted_invoice_url"),
                completed_at=datetime.now(UTC),
            )
        )


async def invoice_failed(session: AsyncSession, invoice: Any) -> None:
    subscription_id = _invoice_subscription(invoice)
    account = await _account_by_subscription(session, subscription_id)
    if account is not None:
        account.plan_status = "past_due"


async def _account_by_subscription(
    session: AsyncSession, subscription_id: str | None
) -> BillingAccount | None:
    if not subscription_id:
        return None
    return await session.scalar(
        select(BillingAccount)
        .where(BillingAccount.stripe_subscription_id == subscription_id)
        .with_for_update()
    )


def _subscription_price(subscription: Any) -> str | None:
    items = _get(_get(subscription, "items") or {}, "data") or []
    return _id(_get(items[0], "price")) if items else None


async def subscription_updated(session: AsyncSession, subscription: Any) -> None:
    account = await _account_by_subscription(session, _id(subscription))
    if account is None:
        user_id = await _user_for(session, subscription)
        if user_id is None:
            return
        account, _ = await ledger.lock_account(session, user_id, roll=False)
        account.stripe_subscription_id = _id(subscription)
    account.cancel_at_period_end = bool(_get(subscription, "cancel_at_period_end"))
    status = _get(subscription, "status")
    if status in {"past_due", "unpaid", "incomplete"}:
        account.plan_status = "past_due"
    elif status in {"active", "trialing"}:
        account.plan_status = "active"
    plan = await _plan_for(
        session, _subscription_price(subscription), _metadata(subscription)
    )
    if (
        plan is not None
        and plan.id != account.plan_id
        and account.plan_source == "stripe"
    ):
        _locked, user = await ledger.lock_account(session, account.user_id, roll=False)
        await ledger.change_plan(
            session,
            account,
            user,
            plan,
            source="stripe",
            reason="Plan changed",
            new_period=False,
        )


async def subscription_deleted(session: AsyncSession, subscription: Any) -> None:
    account = await _account_by_subscription(session, _id(subscription))
    if account is None:
        return
    _locked, user = await ledger.lock_account(session, account.user_id, roll=False)
    free = await ledger.default_plan(session)
    await ledger.change_plan(
        session, account, user, free, source="default", reason="Subscription ended"
    )
    account.stripe_subscription_id = None
    account.cancel_at_period_end = False
