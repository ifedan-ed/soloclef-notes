"""Starting Stripe payments: Checkout for a plan or a pack, the Customer Portal,
and plan changes on an existing subscription.

Amounts always come from the database (``billing_plans.price_cents``,
``billing_packs.price_cents``); the client only names which plan or pack.
"""

from __future__ import annotations

import logging
from typing import Any

from fastapi import HTTPException, status
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from stripe import InvalidRequestError, StripeClient, StripeError

from app.config import config
from app.db import User

from ..persistence.models import (
    BillingAccount,
    BillingPack,
    BillingPayment,
    BillingPlan,
)
from ..units import micros_to_credits

logger = logging.getLogger(__name__)

PURCHASE_PACK = "billing_pack"
PURCHASE_PLAN = "billing_plan"


def _frontend(path: str) -> str:
    base = (config.NEXT_FRONTEND_URL or "").rstrip("/")
    if not base:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="NEXT_FRONTEND_URL is not configured.",
        )
    return f"{base}{path}"


def plan_name(plan: BillingPlan) -> str:
    content = plan.content or {}
    return str((content.get("en") or {}).get("name") or plan.slug.title())


def _bad_gateway(what: str, exc: Exception) -> HTTPException:
    logger.warning("Stripe %s failed: %s", what, exc)
    return HTTPException(
        status_code=status.HTTP_502_BAD_GATEWAY,
        detail={
            "code": "STRIPE_ERROR",
            "message": "The card payment service did not answer. Try again.",
        },
    )


async def ensure_customer(
    session: AsyncSession, client: StripeClient, user: User
) -> str:
    """The user's Customer in the key's mode; a stale (other-mode) id is replaced."""
    if user.stripe_customer_id:
        try:
            customer = client.v1.customers.retrieve(user.stripe_customer_id)
            if not getattr(customer, "deleted", False):
                return user.stripe_customer_id
        except InvalidRequestError:
            pass
    customer = client.v1.customers.create(
        params={"email": user.email, "metadata": {"user_id": str(user.id)}}
    )
    locked = (
        (
            await session.execute(
                select(User).where(User.id == user.id).with_for_update(of=User)
            )
        )
        .unique()
        .scalar_one()
    )
    locked.stripe_customer_id = str(customer.id)
    await session.flush()
    return str(customer.id)


async def ensure_plan_price(
    session: AsyncSession, client: StripeClient, plan: BillingPlan
) -> str:
    """A monthly recurring Price for the plan's current amount.

    Created when missing, when the admin changed the price (existing
    subscribers keep theirs), or when the key cannot see the stored one.
    """
    if plan.stripe_price_id and plan.stripe_price_cents == plan.price_cents:
        try:
            price = client.v1.prices.retrieve(plan.stripe_price_id)
            if getattr(price, "active", True):
                return plan.stripe_price_id
        except InvalidRequestError:
            pass
    product = client.v1.products.create(
        params={
            "name": plan_name(plan),
            "metadata": {"plan_id": str(plan.id), "plan_slug": plan.slug},
        }
    )
    price = client.v1.prices.create(
        params={
            "product": str(product.id),
            "currency": plan.currency or "usd",
            "unit_amount": int(plan.price_cents),
            "recurring": {"interval": "month"},
            "metadata": {"plan_id": str(plan.id)},
        }
    )
    plan.stripe_price_id = str(price.id)
    plan.stripe_price_cents = int(plan.price_cents)
    await session.flush()
    return plan.stripe_price_id


def _metadata(user: User, **extra: Any) -> dict[str, str]:
    return {"user_id": str(user.id), **{k: str(v) for k, v in extra.items()}}


async def checkout_plan(
    session: AsyncSession, client: StripeClient, user: User, plan: BillingPlan
) -> str:
    """A Checkout Session that starts a subscription to ``plan``."""
    if plan.price_cents <= 0 or not plan.active or not plan.public:
        raise HTTPException(
            status_code=400,
            detail={
                "code": "PLAN_NOT_PURCHASABLE",
                "message": "This plan can't be bought.",
            },
        )
    try:
        customer = await ensure_customer(session, client, user)
        price_id = await ensure_plan_price(session, client, plan)
        metadata = _metadata(user, plan_id=plan.id, purchase_type=PURCHASE_PLAN)
        checkout = client.v1.checkout.sessions.create(
            params={
                "mode": "subscription",
                "customer": customer,
                "client_reference_id": str(user.id),
                "line_items": [{"price": price_id, "quantity": 1}],
                "metadata": metadata,
                "subscription_data": {"metadata": metadata},
                "success_url": _frontend(
                    "/billing?checkout=success&session_id={CHECKOUT_SESSION_ID}"
                ),
                "cancel_url": _frontend("/billing?checkout=cancel"),
            }
        )
    except StripeError as exc:
        raise _bad_gateway("subscription checkout", exc) from exc
    session.add(
        BillingPayment(
            user_id=user.id,
            provider="stripe",
            kind="subscription",
            provider_ref=str(checkout.id),
            status="pending",
            amount_cents=int(plan.price_cents),
            currency=plan.currency or "usd",
            plan_id=plan.id,
            months=1,
            checkout_url=checkout.url,
        )
    )
    await session.flush()
    return str(checkout.url)


async def checkout_pack(
    session: AsyncSession, client: StripeClient, user: User, pack: BillingPack
) -> str:
    """A one-off Checkout Session for a credit pack, priced from the pack row."""
    if not pack.active:
        raise HTTPException(
            status_code=400,
            detail={
                "code": "PACK_NOT_AVAILABLE",
                "message": "This pack isn't available.",
            },
        )
    name = str(
        ((pack.content or {}).get("en") or {}).get("name")
        or f"{micros_to_credits(pack.credits_micros):g} credits"
    )
    try:
        customer = await ensure_customer(session, client, user)
        checkout = client.v1.checkout.sessions.create(
            params={
                "mode": "payment",
                "customer": customer,
                "client_reference_id": str(user.id),
                "line_items": [
                    {
                        "quantity": 1,
                        "price_data": {
                            "currency": pack.currency or "usd",
                            "unit_amount": int(pack.price_cents),
                            "product_data": {"name": name},
                        },
                    }
                ],
                "metadata": _metadata(
                    user, pack_id=pack.id, purchase_type=PURCHASE_PACK
                ),
                "invoice_creation": {"enabled": True},
                "success_url": _frontend(
                    "/billing?checkout=success&session_id={CHECKOUT_SESSION_ID}"
                ),
                "cancel_url": _frontend("/billing?checkout=cancel"),
            }
        )
    except StripeError as exc:
        raise _bad_gateway("pack checkout", exc) from exc
    session.add(
        BillingPayment(
            user_id=user.id,
            provider="stripe",
            kind="pack",
            provider_ref=str(checkout.id),
            status="pending",
            amount_cents=int(pack.price_cents),
            currency=pack.currency or "usd",
            credits_micros=int(pack.credits_micros),
            pack_id=pack.id,
            checkout_url=checkout.url,
        )
    )
    await session.flush()
    return str(checkout.url)


async def portal_url(session: AsyncSession, client: StripeClient, user: User) -> str:
    try:
        customer = await ensure_customer(session, client, user)
        portal = client.v1.billing_portal.sessions.create(
            params={"customer": customer, "return_url": _frontend("/billing")}
        )
    except StripeError as exc:
        raise _bad_gateway("customer portal", exc) from exc
    return str(portal.url)


async def change_subscription_plan(
    session: AsyncSession,
    client: StripeClient,
    account: BillingAccount,
    plan: BillingPlan,
) -> None:
    """Move an existing subscription to ``plan``'s price, prorated by Stripe.

    The webhook (``customer.subscription.updated``) applies the change to the
    account; it is applied here too so the page updates at once.
    """
    try:
        price_id = await ensure_plan_price(session, client, plan)
        subscription = client.v1.subscriptions.retrieve(account.stripe_subscription_id)
        item_id = subscription["items"]["data"][0]["id"]
        client.v1.subscriptions.update(
            account.stripe_subscription_id,
            params={
                "items": [{"id": item_id, "price": price_id}],
                "proration_behavior": "create_prorations",
                "cancel_at_period_end": False,
                "metadata": {
                    "user_id": str(account.user_id),
                    "plan_id": str(plan.id),
                    "purchase_type": PURCHASE_PLAN,
                },
            },
        )
    except StripeError as exc:
        raise _bad_gateway("plan change", exc) from exc


async def set_cancel_at_period_end(
    client: StripeClient, account: BillingAccount, cancel: bool
) -> None:
    try:
        client.v1.subscriptions.update(
            account.stripe_subscription_id, params={"cancel_at_period_end": cancel}
        )
    except StripeError as exc:
        raise _bad_gateway("cancellation", exc) from exc
