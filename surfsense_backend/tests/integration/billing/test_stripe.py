"""Stripe in test mode: checkout against stripe-mock, webhooks signed like Stripe signs them.

stripe-mock (``STRIPE_MOCK_URL``, default http://bill-stripe-mock:12111) answers
the API with fixtures; webhooks are built here and signed with the webhook
secret exactly as Stripe does (``t=<ts>,v1=<hmac-sha256("<ts>.<body>")>``).
"""

from __future__ import annotations

import hashlib
import hmac
import json
import os
import time
import uuid

import httpx
import pytest
from sqlalchemy import select

from app.billing import ledger
from app.billing.persistence.models import BillingPack, BillingPayment, BillingPlan
from app.billing.settings_store import save_settings
from app.billing.units import credits_to_micros

pytestmark = pytest.mark.integration

SECRET = "whsec_test_secret"
MOCK = os.getenv("STRIPE_MOCK_URL", "http://bill-stripe-mock:12111")


@pytest.fixture
async def stripe_on(db_session, monkeypatch, set_mode):
    try:
        httpx.get(MOCK, timeout=2)
    except httpx.HTTPError:
        pytest.skip("stripe-mock is not running")
    monkeypatch.setenv("STRIPE_API_BASE", MOCK)
    monkeypatch.setattr("app.config.config.NEXT_FRONTEND_URL", "https://notes.example")
    await set_mode("live")
    await save_settings(
        db_session,
        values={"stripe_enabled": True},
        secrets={"stripe_secret_key": "sk_test_123", "stripe_webhook_secret": SECRET},
    )


def signed(
    event: dict, *, at: int | None = None, secret: str = SECRET
) -> tuple[bytes, dict]:
    body = json.dumps(event).encode()
    ts = at or int(time.time())
    sig = hmac.new(
        secret.encode(), f"{ts}.".encode() + body, hashlib.sha256
    ).hexdigest()
    return body, {
        "Stripe-Signature": f"t={ts},v1={sig}",
        "Content-Type": "application/json",
    }


def event(kind: str, obj: dict, event_id: str | None = None) -> dict:
    return {
        "id": event_id or f"evt_{uuid.uuid4().hex[:16]}",
        "object": "event",
        "type": kind,
        "api_version": "2025-01-27.acacia",
        "created": int(time.time()),
        "data": {"object": obj},
    }


async def _pack(db_session):
    return await db_session.scalar(select(BillingPack).order_by(BillingPack.sort_order))


async def test_pack_checkout_prices_from_the_database(
    db_session, db_user, stripe_on, api
):
    pack = await _pack(db_session)
    async with api(db_user) as client:
        response = await client.post(
            "/api/v1/billing/stripe/checkout",
            json={
                "kind": "pack",
                "pack_id": pack.id,
                "price_cents": 1,
                "credits": 99999,
            },
        )
    assert response.status_code == 200, response.text
    assert response.json()["url"]
    payment = await db_session.scalar(
        select(BillingPayment).where(BillingPayment.user_id == db_user.id)
    )
    assert payment.amount_cents == pack.price_cents == 500
    assert payment.credits_micros == credits_to_micros(500)


async def test_subscription_checkout_creates_a_price_for_the_plan(
    db_session, db_user, stripe_on, api
):
    plan = await db_session.scalar(
        select(BillingPlan).where(BillingPlan.slug == "plus")
    )
    async with api(db_user) as client:
        response = await client.post(
            "/api/v1/billing/stripe/checkout", json={"kind": "plan", "plan_id": plan.id}
        )
    assert response.status_code == 200, response.text
    await db_session.refresh(plan)
    assert plan.stripe_price_id and plan.stripe_price_cents == 900


async def _pending_pack_payment(db_session, user, ref):
    pack = await _pack(db_session)
    payment = BillingPayment(
        user_id=user.id,
        provider="stripe",
        kind="pack",
        provider_ref=ref,
        status="pending",
        amount_cents=pack.price_cents,
        credits_micros=pack.credits_micros,
        pack_id=pack.id,
    )
    db_session.add(payment)
    await db_session.flush()
    return payment


def _paid_checkout(ref, user, *, mode="payment"):
    return {
        "id": ref,
        "object": "checkout.session",
        "mode": mode,
        "payment_status": "paid",
        "status": "complete",
        "customer": None,
        "client_reference_id": str(user.id),
        # Metadata a tampered request might try: the grant ignores it.
        "metadata": {
            "user_id": str(user.id),
            "purchase_type": "billing_pack",
            "credits_micros": "999999999",
        },
    }


async def test_a_paid_pack_is_granted_once_and_replays_are_ignored(
    db_session, db_user, stripe_on, api
):
    await _pending_pack_payment(db_session, db_user, "cs_test_pack1")
    evt = event(
        "checkout.session.completed",
        _paid_checkout("cs_test_pack1", db_user),
        "evt_pack_1",
    )
    async with api(None) as client:
        body, headers = signed(evt)
        first = await client.post(
            "/api/v1/billing/stripe/webhook", content=body, headers=headers
        )
        body, headers = signed(evt)
        replay = await client.post(
            "/api/v1/billing/stripe/webhook", content=body, headers=headers
        )
        # Same checkout, new event id (Stripe's async_payment_succeeded): still once.
        body, headers = signed(
            event(
                "checkout.session.async_payment_succeeded",
                _paid_checkout("cs_test_pack1", db_user),
            )
        )
        other = await client.post(
            "/api/v1/billing/stripe/webhook", content=body, headers=headers
        )
    assert (
        first.status_code == 200
        and replay.json().get("duplicate") is True
        and other.status_code == 200
    )
    _account, user = await ledger.lock_account(db_session, db_user.id)
    assert user.credit_micros_balance == credits_to_micros(500)


async def test_bad_or_stale_signatures_are_rejected(
    db_session, db_user, stripe_on, api
):
    await _pending_pack_payment(db_session, db_user, "cs_test_pack2")
    evt = event("checkout.session.completed", _paid_checkout("cs_test_pack2", db_user))
    async with api(None) as client:
        body, headers = signed(evt, secret="whsec_wrong")
        wrong = await client.post(
            "/api/v1/billing/stripe/webhook", content=body, headers=headers
        )
        body, headers = signed(evt, at=int(time.time()) - 3600)
        stale = await client.post(
            "/api/v1/billing/stripe/webhook", content=body, headers=headers
        )
        missing = await client.post("/api/v1/billing/stripe/webhook", content=body)
    assert wrong.status_code == stale.status_code == missing.status_code == 400
    _a, user = await ledger.lock_account(db_session, db_user.id)
    assert user.credit_micros_balance == 0


def _invoice(invoice_id, user, plan, reason="subscription_create", sub="sub_test_1"):
    now = int(time.time())
    return {
        "id": invoice_id,
        "object": "invoice",
        "billing_reason": reason,
        "subscription": sub,
        "customer": "cus_x",
        "amount_paid": plan.price_cents,
        "currency": "usd",
        "lines": {
            "object": "list",
            "data": [
                {
                    "id": "il_1",
                    "object": "line_item",
                    "metadata": {"user_id": str(user.id), "plan_id": str(plan.id)},
                    "period": {"start": now, "end": now + 30 * 86400},
                }
            ],
        },
    }


async def test_subscription_invoices_start_and_renew_the_plan(
    db_session, db_user, stripe_on, api
):
    plan = await db_session.scalar(select(BillingPlan).where(BillingPlan.slug == "pro"))
    async with api(None) as client:
        body, headers = signed(event("invoice.paid", _invoice("in_1", db_user, plan)))
        assert (
            await client.post(
                "/api/v1/billing/stripe/webhook", content=body, headers=headers
            )
        ).status_code == 200
    account, _ = await ledger.lock_account(db_session, db_user.id)
    assert account.plan_id == plan.id and account.plan_source == "stripe"
    assert account.plan_credits_micros == credits_to_micros(5000)
    account.plan_credits_micros = credits_to_micros(12)
    await db_session.flush()
    async with api(None) as client:
        for _ in range(2):  # the renewal arrives twice (new event ids): granted once
            body, headers = signed(
                event(
                    "invoice.paid",
                    _invoice("in_2", db_user, plan, "subscription_cycle"),
                )
            )
            await client.post(
                "/api/v1/billing/stripe/webhook", content=body, headers=headers
            )
    account, _ = await ledger.lock_account(db_session, db_user.id)
    assert account.plan_credits_micros == credits_to_micros(
        5000
    )  # no rollover of the 12

    async with api(None) as client:
        body, headers = signed(
            event(
                "customer.subscription.deleted",
                {"id": "sub_test_1", "object": "subscription", "status": "canceled"},
            )
        )
        assert (
            await client.post(
                "/api/v1/billing/stripe/webhook", content=body, headers=headers
            )
        ).status_code == 200
    account, _ = await ledger.lock_account(db_session, db_user.id)
    free = await db_session.scalar(
        select(BillingPlan).where(BillingPlan.slug == "free")
    )
    assert account.plan_id == free.id and account.stripe_subscription_id is None


async def test_card_payments_are_off_until_enabled(db_session, db_user, api, set_mode):
    pack = await _pack(db_session)
    async with api(db_user) as client:
        response = await client.post(
            "/api/v1/billing/stripe/checkout", json={"kind": "pack", "pack_id": pack.id}
        )
    assert response.status_code == 503
