"""A person's own billing: plan, balances, usage, payments, and buying.

Everything is scoped to the signed-in user; no endpoint takes a user id.
Amounts come from the plan and pack rows, never from the request.
"""

from __future__ import annotations

from datetime import UTC, datetime
from decimal import Decimal
from typing import Literal
from uuid import uuid4

from fastapi import APIRouter, Depends, HTTPException, Query, Request, Response
from pydantic import BaseModel, Field
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.config import config
from app.db import get_async_session
from app.users import require_session_context

from .. import ledger
from ..crypto import provider as crypto_p
from ..crypto.btcpay import CryptoBusyError
from ..crypto.gateway import crypto_provider, crypto_ready
from ..crypto.settlement import apply_update
from ..persistence.models import (
    BillingActionPrice,
    BillingPack,
    BillingPayment,
    BillingPlan,
    BillingUsage,
)
from ..settings_store import load_settings
from ..stripe_billing import flows as stripe_flows
from ..stripe_billing.client import stripe_client, stripe_ready
from ..stripe_billing.events import fulfil_checkout
from ..units import micros_to_credits
from .presenters import (
    account_out,
    pack_out,
    payment_out,
    plan_out,
    price_out,
    usage_out,
)

router = APIRouter(prefix="/billing", tags=["billing"])


def _locale(request: Request) -> str:
    raw = (
        request.headers.get("x-locale")
        or request.headers.get("accept-language")
        or "en"
    )
    return raw.split(",")[0].split("-")[0].strip().lower() or "en"


async def _catalog(session: AsyncSession, locale: str) -> dict:
    plans = (
        (
            await session.execute(
                select(BillingPlan)
                .where(BillingPlan.active.is_(True))
                .order_by(BillingPlan.sort_order)
            )
        )
        .scalars()
        .all()
    )
    prices = (
        (
            await session.execute(
                select(BillingActionPrice).order_by(BillingActionPrice.sort_order)
            )
        )
        .scalars()
        .all()
    )
    packs = (
        (
            await session.execute(
                select(BillingPack)
                .where(BillingPack.active.is_(True))
                .order_by(BillingPack.sort_order)
            )
        )
        .scalars()
        .all()
    )
    return {
        "plans": [plan_out(p, locale) for p in plans if p.public],
        "prices": [price_out(p, locale) for p in prices],
        "packs": [pack_out(p, locale) for p in packs],
    }


def _crypto_options(settings) -> dict:
    return {
        "enabled": crypto_ready(settings),
        "methods": list(settings.get("crypto_payment_methods") or []),
        "month_options": [int(m) for m in settings.get("crypto_month_options") or [1]],
        "discounts": {
            str(k): v for k, v in (settings.get("crypto_discounts") or {}).items()
        },
    }


@router.get("/catalog")
async def public_catalog(
    request: Request, session: AsyncSession = Depends(get_async_session)
):
    """Plans, per-action prices and packs, for the pricing page (no sign-in)."""
    return await _catalog(session, _locale(request))


@router.get("/public/catalog")
async def public_catalog_for_site(
    request: Request,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
):
    """The public site's price list (``PublicCatalog`` in surfsense_web/lib/pricing).

    ``mode`` tells the site whether billing is live, and ``payments`` which
    methods can actually take money; buy buttons show only when both allow it
    (otherwise "Coming soon"), and the site names only the methods offered.
    Cached for a minute.
    """
    locale = _locale(request)
    catalog = await _catalog(session, locale)
    settings = await load_settings(session)
    mode = settings.mode
    plans = catalog["plans"]
    flagged = {
        p["slug"]
        for p in plans
        if (await session.get(BillingPlan, p["id"])).content.get("highlighted")
    }
    if not flagged and len(plans) > 1:
        flagged = {plans[1]["slug"]}
    response.headers["Cache-Control"] = "public, max-age=60"
    crypto = _crypto_options(settings)
    return {
        "mode": mode,
        "currency": "USD",
        "payments": {
            "card": stripe_ready(settings),
            "crypto": crypto["enabled"],
            "cryptoMonthOptions": crypto["month_options"] if crypto["enabled"] else [],
        },
        "plans": [
            {
                "slug": p["slug"],
                "name": p["name"],
                "tagline": p["tagline"],
                "priceCents": p["price_cents"],
                "monthlyCredits": p["monthly_credits"],
                "features": p["features"],
                "highlighted": p["slug"] in flagged,
            }
            for p in plans
        ],
        "packs": [
            {
                "slug": k["slug"],
                "name": k["name"],
                "credits": k["credits"],
                "priceCents": k["price_cents"],
            }
            for k in catalog["packs"]
        ],
        "prices": [
            {
                "action": r["action"],
                "label": r["label"],
                "credits": r["credits"],
                "unit": r["unit"],
            }
            for r in catalog["prices"]
        ],
    }


@router.get("/me")
async def my_billing(
    request: Request,
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
):
    settings = await load_settings(session)
    account, user = await ledger.lock_account(session, auth.user.id)
    plan = await session.get(BillingPlan, account.plan_id)
    balances = ledger.balances(account, user)
    await session.commit()
    locale = _locale(request)
    catalog = await _catalog(session, locale)
    period_used = await session.scalar(
        select(func.coalesce(func.sum(BillingUsage.charged_micros), 0)).where(
            BillingUsage.user_id == auth.user.id,
            BillingUsage.created_at >= account.period_start,
            BillingUsage.status == "settled",
        )
    )
    return {
        "mode": settings.mode,
        "is_admin": ledger.is_config_admin(user),
        "exempt": account.exempt or ledger.is_config_admin(user),
        "plan": plan_out(plan, locale) if plan else None,
        "account": account_out(account),
        "balances": {
            "plan_credits": micros_to_credits(balances.plan_micros),
            "plan_allowance": micros_to_credits(plan.monthly_credits_micros)
            if plan
            else 0,
            "topup_credits": micros_to_credits(balances.topup_micros),
            "reserved_credits": micros_to_credits(balances.reserved_micros),
            "spendable_credits": micros_to_credits(max(0, balances.spendable_micros)),
            "period_used_credits": micros_to_credits(int(period_used or 0)),
        },
        "low_balance_credits": settings.get("low_balance_credits"),
        "payments": {
            "card": stripe_ready(settings),
            "crypto": _crypto_options(settings),
        },
        **catalog,
    }


@router.get("/usage")
async def my_usage(
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
    limit: int = Query(50, ge=1, le=200),
    offset: int = Query(0, ge=0),
    action: str | None = None,
    notebook_id: int | None = None,
    days: int = Query(30, ge=1, le=366),
):
    since = datetime.now(UTC).timestamp() - days * 86400
    filters = [
        BillingUsage.user_id == auth.user.id,
        BillingUsage.created_at >= datetime.fromtimestamp(since, UTC),
        BillingUsage.status.in_(["settled", "reserved", "denied", "refunded"]),
    ]
    if action:
        filters.append(BillingUsage.action == action)
    if notebook_id is not None:
        filters.append(BillingUsage.notebook_id == notebook_id)
    rows = (
        (
            await session.execute(
                select(BillingUsage)
                .where(*filters)
                .order_by(BillingUsage.created_at.desc())
                .offset(offset)
                .limit(limit)
            )
        )
        .scalars()
        .all()
    )
    summary = (
        await session.execute(
            select(
                BillingUsage.action,
                func.count(BillingUsage.id),
                func.coalesce(func.sum(BillingUsage.charged_micros), 0),
            )
            .where(*filters, BillingUsage.status == "settled")
            .group_by(BillingUsage.action)
        )
    ).all()
    total = await session.scalar(select(func.count(BillingUsage.id)).where(*filters))
    return {
        "items": [usage_out(r) for r in rows],
        "total": int(total or 0),
        "by_action": [
            {"action": a, "count": int(n), "credits": micros_to_credits(int(m))}
            for a, n, m in summary
        ],
    }


@router.get("/payments")
async def my_payments(
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
    limit: int = Query(50, ge=1, le=200),
):
    rows = (
        (
            await session.execute(
                select(BillingPayment)
                .where(BillingPayment.user_id == auth.user.id)
                .order_by(BillingPayment.created_at.desc())
                .limit(limit)
            )
        )
        .scalars()
        .all()
    )
    return {"items": [payment_out(r) for r in rows]}


class CheckoutBody(BaseModel):
    kind: Literal["plan", "pack"]
    plan_id: int | None = None
    pack_id: int | None = None


async def _plan_or_404(session: AsyncSession, plan_id: int | None) -> BillingPlan:
    plan = await session.get(BillingPlan, plan_id) if plan_id else None
    if plan is None or not plan.active:
        raise HTTPException(status_code=404, detail="Plan not found")
    return plan


async def _pack_or_404(session: AsyncSession, pack_id: int | None) -> BillingPack:
    pack = await session.get(BillingPack, pack_id) if pack_id else None
    if pack is None or not pack.active:
        raise HTTPException(status_code=404, detail="Pack not found")
    return pack


def _card_or_503(settings) -> None:
    if not stripe_ready(settings):
        raise HTTPException(
            status_code=503,
            detail={
                "code": "CARD_PAYMENTS_OFF",
                "message": "Card payments are not available yet.",
            },
        )


@router.post("/stripe/checkout")
async def stripe_checkout(
    body: CheckoutBody,
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
):
    settings = await load_settings(session)
    _card_or_503(settings)
    client = stripe_client(settings)
    if body.kind == "plan":
        plan = await _plan_or_404(session, body.plan_id)
        account, _ = await ledger.lock_account(session, auth.user.id)
        if account.stripe_subscription_id:
            raise HTTPException(
                status_code=409,
                detail={
                    "code": "ALREADY_SUBSCRIBED",
                    "message": "You already have a subscription; change its plan instead.",
                },
            )
        url = await stripe_flows.checkout_plan(session, client, auth.user, plan)
    else:
        pack = await _pack_or_404(session, body.pack_id)
        url = await stripe_flows.checkout_pack(session, client, auth.user, pack)
    await session.commit()
    return {"url": url}


@router.post("/stripe/portal")
async def stripe_portal(
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
):
    settings = await load_settings(session)
    _card_or_503(settings)
    url = await stripe_flows.portal_url(session, stripe_client(settings), auth.user)
    await session.commit()
    return {"url": url}


class ChangePlanBody(BaseModel):
    plan_id: int


@router.post("/stripe/change-plan")
async def stripe_change_plan(
    body: ChangePlanBody,
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
):
    """Upgrade or downgrade an existing subscription (prorated). Choosing the
    free default plan cancels at the end of the paid period."""
    settings = await load_settings(session)
    _card_or_503(settings)
    client = stripe_client(settings)
    plan = await _plan_or_404(session, body.plan_id)
    account, user = await ledger.lock_account(session, auth.user.id)
    if not account.stripe_subscription_id:
        raise HTTPException(
            status_code=409,
            detail={
                "code": "NO_SUBSCRIPTION",
                "message": "There is no subscription to change.",
            },
        )
    if plan.is_default or plan.price_cents <= 0:
        await stripe_flows.set_cancel_at_period_end(client, account, True)
        account.cancel_at_period_end = True
    else:
        await stripe_flows.change_subscription_plan(session, client, account, plan)
        if plan.id != account.plan_id:
            await ledger.change_plan(
                session,
                account,
                user,
                plan,
                source="stripe",
                reason="Plan changed",
                new_period=False,
            )
        account.cancel_at_period_end = False
    await session.commit()
    return {"ok": True}


class CancelBody(BaseModel):
    cancel: bool = True


@router.post("/stripe/cancel")
async def stripe_cancel(
    body: CancelBody,
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
):
    settings = await load_settings(session)
    _card_or_503(settings)
    account, _ = await ledger.lock_account(session, auth.user.id)
    if not account.stripe_subscription_id:
        raise HTTPException(
            status_code=409,
            detail={"code": "NO_SUBSCRIPTION", "message": "There is no subscription."},
        )
    await stripe_flows.set_cancel_at_period_end(
        stripe_client(settings), account, body.cancel
    )
    account.cancel_at_period_end = body.cancel
    await session.commit()
    return {"ok": True}


@router.get("/stripe/return")
async def stripe_return(
    session_id: str,
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
):
    """Fulfil a finished checkout at once when the buyer lands back, rather
    than waiting for the webhook (both are idempotent)."""
    payment = await session.scalar(
        select(BillingPayment).where(
            BillingPayment.provider == "stripe",
            BillingPayment.provider_ref == session_id,
        )
    )
    if payment is None or payment.user_id != auth.user.id:
        raise HTTPException(status_code=404, detail="Checkout not found")
    settings = await load_settings(session)
    if payment.status == "pending" and settings.get("stripe_secret_key"):
        checkout = stripe_client(settings).v1.checkout.sessions.retrieve(session_id)
        await fulfil_checkout(session, checkout)
        await session.commit()
        await session.refresh(payment)
    return payment_out(payment)


class CryptoBody(BaseModel):
    kind: Literal["plan", "pack"]
    plan_id: int | None = None
    pack_id: int | None = None
    months: int = Field(1, ge=1, le=36)
    # A BTCPay payment method id (BTC-CHAIN, USDT-TRON, USDT-POLYGON) to offer
    # only that one; by default the buyer chooses on BTCPay's checkout.
    pay_currency: str | None = None


def crypto_price_cents(plan: BillingPlan, months: int, discounts: dict) -> int:
    pct = float((discounts or {}).get(str(months)) or 0)
    return round(plan.price_cents * months * (100 - pct) / 100)


@router.post("/crypto/payments")
async def crypto_payment(
    body: CryptoBody,
    request: Request,
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
):
    """Start a USDT payment: returns the address and exact amount to send."""
    settings = await load_settings(session)
    if not crypto_ready(settings):
        raise HTTPException(
            status_code=503,
            detail={
                "code": "CRYPTO_PAYMENTS_OFF",
                "message": "Crypto payments are not available yet.",
            },
        )
    methods = [m.upper() for m in settings.get("crypto_payment_methods") or []]
    if body.pay_currency and body.pay_currency.upper() not in methods:
        raise HTTPException(
            status_code=400,
            detail={
                "code": "CURRENCY_NOT_ACCEPTED",
                "message": "That currency isn't accepted.",
            },
        )
    order_id = f"sn-{uuid4().hex[:20]}"
    if body.kind == "plan":
        plan = await _plan_or_404(session, body.plan_id)
        if plan.price_cents <= 0:
            raise HTTPException(
                status_code=400,
                detail={
                    "code": "PLAN_NOT_PURCHASABLE",
                    "message": "This plan can't be bought.",
                },
            )
        months = int(body.months)
        if months not in [int(m) for m in settings.get("crypto_month_options") or [1]]:
            raise HTTPException(
                status_code=400,
                detail={
                    "code": "MONTHS_NOT_OFFERED",
                    "message": "That number of months isn't offered.",
                },
            )
        account, _ = await ledger.lock_account(session, auth.user.id)
        if account.stripe_subscription_id and not account.cancel_at_period_end:
            raise HTTPException(
                status_code=409,
                detail={
                    "code": "ALREADY_SUBSCRIBED",
                    "message": "Cancel your card subscription before prepaying by crypto.",
                },
            )
        amount_cents = crypto_price_cents(
            plan, months, settings.get("crypto_discounts") or {}
        )
        row = {
            "kind": "plan_months",
            "plan_id": plan.id,
            "months": months,
            "credits_micros": 0,
        }
        description = f"{stripe_flows.plan_name(plan)} x {months} month(s)"
    else:
        pack = await _pack_or_404(session, body.pack_id)
        amount_cents = int(pack.price_cents)
        row = {
            "kind": "pack",
            "pack_id": pack.id,
            "credits_micros": int(pack.credits_micros),
        }
        description = f"{micros_to_credits(pack.credits_micros):g} credits"
    gateway = crypto_provider(settings)
    callback = f"{(config.BACKEND_URL or '').rstrip('/')}/api/v1/billing/crypto/webhook"
    try:
        created = await gateway.create_payment(
            order_id=order_id,
            price_usd=Decimal(amount_cents) / 100,
            pay_currency=body.pay_currency,
            description=description,
            callback_url=callback,
            redirect_url=f"{(config.NEXT_FRONTEND_URL or '').rstrip('/')}/billing?crypto=done",
        )
    except CryptoBusyError as exc:
        # Suggest a card only while card payments are really on offer.
        message = (
            "Crypto checkout is busy right now. Pay by card, or try again in a few minutes."
            if stripe_ready(settings)
            else "Crypto checkout is busy right now. Try again in a few minutes."
        )
        raise HTTPException(
            status_code=503,
            detail={"code": "CRYPTO_BUSY", "message": message},
        ) from exc
    except crypto_p.ProviderUnavailable as exc:
        raise HTTPException(
            status_code=502,
            detail={
                "code": "CRYPTO_PROVIDER_ERROR",
                "message": "The crypto payment service did not answer. Try again.",
            },
        ) from exc
    payment = BillingPayment(
        user_id=auth.user.id,
        provider=gateway.name,
        provider_ref=created.provider_ref,
        status="pending",
        amount_cents=amount_cents,
        currency="usd",
        details={
            "order_id": order_id,
            "pay_address": created.pay_address,
            "pay_amount": str(created.pay_amount),
            "pay_currency": created.pay_currency,
            "network": created.network,
            "expires_at": created.expires_at.isoformat()
            if created.expires_at
            else None,
            "raw_status": created.raw_status,
            "payment_url": created.extra.get("payment_url"),
        },
        **row,
    )
    session.add(payment)
    await session.commit()
    await session.refresh(payment)
    return payment_out(payment)


@router.get("/crypto/payments/{payment_id}")
async def crypto_payment_status(
    payment_id: int,
    auth: AuthContext = Depends(require_session_context),
    session: AsyncSession = Depends(get_async_session),
):
    """The payment's state; a pending one is re-checked with the provider."""
    payment = await session.get(BillingPayment, payment_id)
    if (
        payment is None
        or payment.user_id != auth.user.id
        or payment.provider == "stripe"
    ):
        raise HTTPException(status_code=404, detail="Payment not found")
    if payment.status in {"pending", "partial"}:
        settings = await load_settings(session)
        gateway = crypto_provider(settings)
        try:
            update = await gateway.poll(payment.provider_ref)
        except crypto_p.ProviderUnavailable:
            update = None
        if update is not None:
            await apply_update(
                session,
                gateway,
                update,
            )
            await session.commit()
            await session.refresh(payment)
    return payment_out(payment)
