"""Catalog and account rows as the apps see them (credits, not micros)."""

from __future__ import annotations

from typing import Any

from ..entitlements import normalized
from ..persistence.models import (
    BillingAccount,
    BillingActionPrice,
    BillingPack,
    BillingPayment,
    BillingPlan,
    BillingUsage,
)
from ..units import micros_to_credits


def localized(content: dict | None, locale: str | None) -> dict[str, Any]:
    content = content or {}
    base = dict(content.get("en") or {})
    if locale and locale != "en":
        base.update({k: v for k, v in (content.get(locale) or {}).items() if v})
    return base


def plan_out(
    plan: BillingPlan, locale: str | None = None, *, admin: bool = False
) -> dict:
    text = localized(plan.content, locale)
    out = {
        "id": plan.id,
        "slug": plan.slug,
        "name": text.get("name") or plan.slug.title(),
        "tagline": text.get("tagline") or "",
        "features": list(text.get("features") or []),
        "price_cents": plan.price_cents,
        "currency": plan.currency,
        "monthly_credits": micros_to_credits(plan.monthly_credits_micros),
        "entitlements": normalized(plan.entitlements),
        "is_default": plan.is_default,
        "public": plan.public,
        "active": plan.active,
        "sort_order": plan.sort_order,
    }
    if admin:
        out["content"] = plan.content or {}
        out["stripe_price_id"] = plan.stripe_price_id
    return out


def price_out(
    row: BillingActionPrice, locale: str | None = None, *, admin: bool = False
) -> dict:
    out = {
        "action": row.action,
        "credits": micros_to_credits(row.micros),
        "unit": row.unit,
        "label": localized(row.content, locale).get("label") or row.action,
        "sort_order": row.sort_order,
    }
    if admin:
        out["content"] = row.content or {}
    return out


def pack_out(
    pack: BillingPack, locale: str | None = None, *, admin: bool = False
) -> dict:
    out = {
        "id": pack.id,
        "slug": pack.slug,
        "name": localized(pack.content, locale).get("name") or pack.slug,
        "credits": micros_to_credits(pack.credits_micros),
        "price_cents": pack.price_cents,
        "currency": pack.currency,
        "active": pack.active,
        "sort_order": pack.sort_order,
    }
    if admin:
        out["content"] = pack.content or {}
    return out


def account_out(account: BillingAccount) -> dict:
    return {
        "plan_id": account.plan_id,
        "plan_source": account.plan_source,
        "plan_status": account.plan_status,
        "period_start": account.period_start,
        "period_end": account.period_end,
        "paid_until": account.plan_paid_until,
        "cancel_at_period_end": account.cancel_at_period_end,
        "has_subscription": bool(account.stripe_subscription_id),
        "exempt": account.exempt,
    }


def usage_out(row: BillingUsage) -> dict:
    return {
        "id": row.id,
        "action": row.action,
        "units": row.units,
        "credits": micros_to_credits(row.charged_micros or row.reserved_micros),
        "mode": row.mode,
        "status": row.status,
        "denial_code": row.denial_code,
        "workspace_id": row.workspace_id,
        "notebook_id": row.notebook_id,
        "created_at": row.created_at,
    }


def payment_out(row: BillingPayment) -> dict:
    details = row.details or {}
    return {
        "id": row.id,
        "provider": row.provider,
        "kind": row.kind,
        "status": row.status,
        "amount_cents": row.amount_cents,
        "currency": row.currency,
        "credits": micros_to_credits(row.credits_micros),
        "plan_id": row.plan_id,
        "months": row.months,
        "receipt_url": row.receipt_url,
        "created_at": row.created_at,
        "completed_at": row.completed_at,
        "crypto": {
            "pay_address": details.get("pay_address"),
            "pay_amount": details.get("pay_amount"),
            "pay_currency": details.get("pay_currency"),
            "network": details.get("network"),
            "actually_paid": details.get("actually_paid"),
            "remaining": details.get("remaining"),
            "expires_at": details.get("expires_at"),
            "payment_url": details.get("payment_url"),
        }
        if row.provider != "stripe"
        else None,
    }
