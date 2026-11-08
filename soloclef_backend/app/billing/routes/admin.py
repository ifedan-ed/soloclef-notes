"""Billing administration: catalog, settings, accounts, model tiers, reports.

Every route requires a billing administrator (superuser or listed in
``BILLING_ADMIN_EMAILS``). Secrets are write-only: they can be set or cleared
but no route returns them.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from typing import Any
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Query
from pydantic import BaseModel, Field
from sqlalchemy import func, or_, select, update
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.config import config
from app.db import Model, User, get_async_session
from app.model_display_names import friendly_name

from .. import ledger, model_tiers
from ..admin_access import require_billing_admin
from ..defaults import MODEL_TIERS, PARSERS, STUDIO_KINDS
from ..persistence.models import (
    BillingAccount,
    BillingActionPrice,
    BillingLedgerEntry,
    BillingPack,
    BillingPlan,
    BillingUsage,
)
from ..seed import reset_prices_and_packs
from ..settings_store import SECRET_ENV, load_settings, save_settings
from ..units import credits_to_micros, micros_to_credits
from .presenters import account_out, pack_out, plan_out, price_out

router = APIRouter(prefix="/billing/admin", tags=["billing-admin"])


def _bad(message: str) -> HTTPException:
    return HTTPException(
        status_code=400, detail={"code": "INVALID", "message": message}
    )


@router.get("/overview")
async def overview(
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    settings = await load_settings(session)
    plans = (
        (await session.execute(select(BillingPlan).order_by(BillingPlan.sort_order)))
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
        (await session.execute(select(BillingPack).order_by(BillingPack.sort_order)))
        .scalars()
        .all()
    )
    return {
        "settings": settings.describe(),
        "secret_keys": list(SECRET_ENV),
        "plans": [plan_out(p, admin=True) for p in plans],
        "prices": [price_out(p, admin=True) for p in prices],
        "packs": [pack_out(p, admin=True) for p in packs],
        "choices": {
            "studio_kinds": list(STUDIO_KINDS),
            "parsers": list(PARSERS),
            "model_tiers": list(MODEL_TIERS),
        },
        "webhooks": {
            "stripe": f"{(config.BACKEND_URL or '').rstrip('/')}/api/v1/billing/stripe/webhook",
            "crypto": f"{(config.BACKEND_URL or '').rstrip('/')}/api/v1/billing/crypto/webhook",
        },
    }


class SettingsBody(BaseModel):
    values: dict[str, Any] = Field(default_factory=dict)
    # key -> new secret; "" or null clears it. Keys not sent are unchanged.
    secrets: dict[str, str | None] = Field(default_factory=dict)


@router.put("/settings")
async def put_settings(
    body: SettingsBody,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    try:
        await save_settings(
            session, values=body.values, secrets=body.secrets, actor=auth.user.id
        )
    except ValueError as exc:
        raise _bad(str(exc)) from exc
    await session.commit()
    return (await load_settings(session)).describe()


class PlanBody(BaseModel):
    slug: str = Field(min_length=1, max_length=64, pattern=r"^[a-z0-9-]+$")
    sort_order: int = 0
    active: bool = True
    public: bool = True
    is_default: bool = False
    price_cents: int = Field(ge=0, le=10_000_000)
    currency: str = "usd"
    monthly_credits: float = Field(ge=0, le=100_000_000)
    entitlements: dict[str, Any] = Field(default_factory=dict)
    content: dict[str, Any] = Field(default_factory=dict)


def _apply_plan(plan: BillingPlan, body: PlanBody) -> None:
    ent = body.entitlements
    for key in ("parsers", "model_tiers", "studio_kinds"):
        if key in ent and not isinstance(ent[key], list):
            raise _bad(f"{key} must be a list")
    plan.slug = body.slug
    plan.sort_order = body.sort_order
    plan.active = body.active
    plan.public = body.public
    plan.is_default = body.is_default
    plan.price_cents = body.price_cents
    plan.currency = body.currency.lower()
    plan.monthly_credits_micros = credits_to_micros(body.monthly_credits)
    plan.entitlements = ent
    plan.content = body.content
    plan.updated_at = datetime.now(UTC)


async def _single_default(session: AsyncSession, plan: BillingPlan) -> None:
    if plan.is_default:
        await session.execute(
            update(BillingPlan)
            .where(BillingPlan.id != plan.id)
            .values(is_default=False)
        )


@router.post("/plans")
async def create_plan(
    body: PlanBody,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    plan = BillingPlan()
    _apply_plan(plan, body)
    session.add(plan)
    await session.flush()
    await _single_default(session, plan)
    await session.commit()
    return plan_out(plan, admin=True)


@router.put("/plans/{plan_id}")
async def put_plan(
    plan_id: int,
    body: PlanBody,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    plan = await session.get(BillingPlan, plan_id)
    if plan is None:
        raise HTTPException(status_code=404, detail="Plan not found")
    if plan.is_default and not body.is_default:
        raise _bad("Make another plan the default first.")
    _apply_plan(plan, body)
    await _single_default(session, plan)
    await session.commit()
    return plan_out(plan, admin=True)


class PriceBody(BaseModel):
    credits: float = Field(ge=0, le=1_000_000)
    unit: str | None = None
    content: dict[str, Any] | None = None


@router.put("/prices/{action}")
async def put_price(
    action: str,
    body: PriceBody,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    row = await session.get(BillingActionPrice, action)
    if row is None:
        raise HTTPException(status_code=404, detail="Unknown action")
    row.micros = credits_to_micros(body.credits)
    if body.unit:
        row.unit = body.unit
    if body.content is not None:
        row.content = body.content
    row.updated_at = datetime.now(UTC)
    await session.commit()
    return price_out(row, admin=True)


class PackBody(BaseModel):
    slug: str = Field(min_length=1, max_length=64, pattern=r"^[a-z0-9-]+$")
    active: bool = True
    sort_order: int = 0
    credits: float = Field(gt=0, le=100_000_000)
    price_cents: int = Field(gt=0, le=10_000_000)
    currency: str = "usd"
    content: dict[str, Any] = Field(default_factory=dict)


def _apply_pack(pack: BillingPack, body: PackBody) -> None:
    pack.slug = body.slug
    pack.active = body.active
    pack.sort_order = body.sort_order
    pack.credits_micros = credits_to_micros(body.credits)
    pack.price_cents = body.price_cents
    pack.currency = body.currency.lower()
    pack.content = body.content
    pack.updated_at = datetime.now(UTC)


@router.post("/packs")
async def create_pack(
    body: PackBody,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    pack = BillingPack()
    _apply_pack(pack, body)
    session.add(pack)
    await session.commit()
    return pack_out(pack, admin=True)


@router.put("/packs/{pack_id}")
async def put_pack(
    pack_id: int,
    body: PackBody,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    pack = await session.get(BillingPack, pack_id)
    if pack is None:
        raise HTTPException(status_code=404, detail="Pack not found")
    _apply_pack(pack, body)
    await session.commit()
    return pack_out(pack, admin=True)


@router.delete("/packs/{pack_id}")
async def delete_pack(
    pack_id: int,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    pack = await session.get(BillingPack, pack_id)
    if pack is not None:
        # Kept for payment history; hidden from buyers.
        pack.active = False
        await session.commit()
    return {"ok": True}


@router.post("/reset-prices")
async def reset_prices(
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    await reset_prices_and_packs(session)
    await session.commit()
    return {"ok": True}


@router.get("/users")
async def list_users(
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
    q: str | None = None,
    limit: int = Query(50, ge=1, le=200),
    offset: int = Query(0, ge=0),
):
    query = select(
        User.id,
        User.email,
        User.credit_micros_balance,
        User.is_verified,
        User.is_superuser,
    ).order_by(User.email)
    if q:
        query = query.where(User.email.ilike(f"%{q}%"))
    users = (await session.execute(query.offset(offset).limit(limit))).all()
    ids = [u.id for u in users]
    accounts = (
        {
            a.user_id: a
            for a in (
                await session.execute(
                    select(BillingAccount).where(BillingAccount.user_id.in_(ids))
                )
            ).scalars()
        }
        if ids
        else {}
    )
    out = []
    for user_id, email, topup, verified, superuser in users:
        account = accounts.get(user_id)
        out.append(
            {
                "user_id": str(user_id),
                "email": email,
                "is_config_admin": bool(superuser)
                or ledger.is_listed_admin(email, verified=bool(verified)),
                "topup_credits": micros_to_credits(topup or 0),
                "plan_credits": micros_to_credits(account.plan_credits_micros)
                if account
                else None,
                "account": account_out(account) if account else None,
            }
        )
    return {"items": out}


class AccountBody(BaseModel):
    plan_id: int | None = None
    # With a plan: when the admin-given plan ends (None = until changed).
    paid_until: datetime | None = None
    exempt: bool | None = None
    exempt_reason: str | None = None


@router.put("/users/{user_id}")
async def put_account(
    user_id: UUID,
    body: AccountBody,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    account, user = await ledger.lock_account(session, user_id)
    if body.plan_id is not None and body.plan_id != account.plan_id:
        plan = await session.get(BillingPlan, body.plan_id)
        if plan is None:
            raise HTTPException(status_code=404, detail="Plan not found")
        if account.stripe_subscription_id:
            raise _bad("This person has a card subscription; change it in Stripe.")
        await ledger.change_plan(
            session,
            account,
            user,
            plan,
            source="default" if plan.is_default else "admin",
            paid_until=body.paid_until,
            reason=f"Set by admin {auth.user.email}",
        )
    if body.exempt is not None:
        account.exempt = body.exempt
        account.exempt_reason = (body.exempt_reason or "")[:300] or None
    await session.commit()
    return account_out(account)


class AdjustBody(BaseModel):
    credits: float = Field(ge=-10_000_000, le=10_000_000)
    bucket: str = Field("topup", pattern="^(topup|plan)$")
    reason: str = Field(min_length=3, max_length=300)


@router.post("/users/{user_id}/adjust")
async def adjust(
    user_id: UUID,
    body: AdjustBody,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    if body.credits == 0:
        raise _bad("Enter a non-zero amount.")
    account, user = await ledger.lock_account(session, user_id)
    await ledger.post(
        session,
        account,
        user,
        bucket=body.bucket,
        delta=credits_to_micros(body.credits),
        kind="admin_adjust",
        reason=body.reason,
        actor=auth.user.id,
    )
    await session.commit()
    return {
        "plan_credits": micros_to_credits(account.plan_credits_micros),
        "topup_credits": micros_to_credits(user.credit_micros_balance),
    }


@router.get("/users/{user_id}/ledger")
async def user_ledger(
    user_id: UUID,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
    limit: int = Query(100, ge=1, le=500),
):
    rows = (
        (
            await session.execute(
                select(BillingLedgerEntry)
                .where(BillingLedgerEntry.user_id == user_id)
                .order_by(BillingLedgerEntry.id.desc())
                .limit(limit)
            )
        )
        .scalars()
        .all()
    )
    return {
        "items": [
            {
                "id": r.id,
                "bucket": r.bucket,
                "credits": micros_to_credits(r.delta_micros),
                "balance_after": micros_to_credits(r.balance_after_micros),
                "kind": r.kind,
                "reason": r.reason,
                "created_at": r.created_at,
            }
            for r in rows
        ]
    }


@router.get("/models")
async def list_model_tiers(
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    """Every model anyone can chat with, and its tier."""
    overrides = await model_tiers.tier_overrides(session)
    names: dict[str, dict] = {}
    for model in config.GLOBAL_MODELS or []:
        name = str(model.get("model_id") or "")
        if name:
            names[name] = {
                "model_id": name,
                "display_name": model.get("display_name") or name,
                "source": "global",
                "tier": overrides.get(name)
                or model_tiers.normalize(model.get("billing_tier")),
            }
    rows = (
        await session.execute(
            select(
                Model.model_id,
                func.max(Model.display_name),
                func.max(Model.billing_tier),
            )
            .where(
                Model.enabled.is_(True),
                or_(Model.supports_chat.is_(True), Model.supports_chat.is_(None)),
            )
            .group_by(Model.model_id)
        )
    ).all()
    for name, display, tier in rows:
        names.setdefault(
            name,
            {
                "model_id": name,
                "display_name": display
                if display and display != name
                else friendly_name(name),
                "source": "connection",
                "tier": overrides.get(name) or model_tiers.normalize(tier),
            },
        )
    return {"items": sorted(names.values(), key=lambda m: m["model_id"])}


class TierBody(BaseModel):
    tier: str = Field(pattern="^(standard|premium)$")


@router.put("/models/{model_id:path}")
async def put_model_tier(
    model_id: str,
    body: TierBody,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    await model_tiers.set_tier(session, model_id, body.tier, actor=auth.user.id)
    await session.commit()
    return {"model_id": model_id, "tier": body.tier}


@router.get("/report")
async def usage_report(
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
    days: int = Query(30, ge=1, le=366),
):
    """Real usage, including what shadow mode would have charged and refused."""
    since = datetime.now(UTC) - timedelta(days=days)
    by_action = (
        await session.execute(
            select(
                BillingUsage.action,
                BillingUsage.mode,
                func.count(BillingUsage.id),
                func.coalesce(func.sum(BillingUsage.units), 0),
                func.coalesce(func.sum(BillingUsage.charged_micros), 0),
                func.count(BillingUsage.denial_code),
                func.coalesce(
                    func.sum(BillingUsage.details["provider_cost_micros"].as_integer()),
                    0,
                ),
            )
            .where(BillingUsage.created_at >= since, BillingUsage.status == "settled")
            .group_by(BillingUsage.action, BillingUsage.mode)
            .order_by(BillingUsage.action)
        )
    ).all()
    by_user = (
        await session.execute(
            select(
                User.email,
                func.count(BillingUsage.id),
                func.coalesce(func.sum(BillingUsage.charged_micros), 0),
            )
            .join(User, User.id == BillingUsage.user_id)
            .where(BillingUsage.created_at >= since, BillingUsage.status == "settled")
            .group_by(User.email)
            .order_by(func.sum(BillingUsage.charged_micros).desc())
            .limit(50)
        )
    ).all()
    denials = (
        await session.execute(
            select(BillingUsage.denial_code, func.count(BillingUsage.id))
            .where(
                BillingUsage.created_at >= since, BillingUsage.denial_code.is_not(None)
            )
            .group_by(BillingUsage.denial_code)
        )
    ).all()
    return {
        "days": days,
        "by_action": [
            {
                "action": a,
                "mode": m,
                "count": int(n),
                "units": int(u),
                "credits": micros_to_credits(int(c)),
                "would_refuse": int(d),
                "provider_cost_usd": round(int(pc) / 1_000_000, 4),
            }
            for a, m, n, u, c, d, pc in by_action
        ],
        "by_user": [
            {"email": e, "count": int(n), "credits": micros_to_credits(int(c))}
            for e, n, c in by_user
        ],
        "denials": [{"code": c, "count": int(n)} for c, n in denials],
    }
