"""Payment webhooks: verified, recorded once, then applied.

Each event id is inserted into ``billing_webhook_events`` in the same
transaction that applies it. A replay (same id) finds the row and does
nothing; a failure rolls both back, so the provider's retry is processed.
Stripe's signature also carries a timestamp, and events older than five
minutes are rejected, so a captured request cannot be replayed later.
"""

from __future__ import annotations

import logging

from fastapi import APIRouter, Depends, HTTPException, Request
from fastapi.responses import PlainTextResponse
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.ext.asyncio import AsyncSession
from stripe import SignatureVerificationError

from app.db import get_async_session

from ..crypto import provider as crypto_p
from ..crypto.gateway import crypto_provider
from ..crypto.settlement import apply_update
from ..persistence.models import BillingWebhookEvent
from ..settings_store import load_settings
from ..stripe_billing import events
from ..stripe_billing.client import stripe_client

logger = logging.getLogger(__name__)
router = APIRouter(prefix="/billing", tags=["billing-webhooks"])


async def _first_time(
    session: AsyncSession, provider: str, event_id: str, event_type: str | None
) -> bool:
    inserted = await session.execute(
        pg_insert(BillingWebhookEvent)
        .values(provider=provider, event_id=event_id, event_type=event_type)
        .on_conflict_do_nothing(index_elements=["provider", "event_id"])
        .returning(BillingWebhookEvent.event_id)
    )
    return inserted.scalar_one_or_none() is not None


@router.post("/stripe/webhook")
async def stripe_webhook(
    request: Request, session: AsyncSession = Depends(get_async_session)
):
    settings = await load_settings(session)
    secret = settings.get("stripe_webhook_secret")
    if not secret or not settings.get("stripe_secret_key"):
        raise HTTPException(
            status_code=503, detail="Stripe webhooks are not configured."
        )
    signature = request.headers.get("Stripe-Signature")
    if not signature:
        raise HTTPException(status_code=400, detail="Missing Stripe-Signature header.")
    payload = await request.body()
    client = stripe_client(settings)
    try:
        event = client.construct_event(payload, signature, secret)
    except (ValueError, SignatureVerificationError) as exc:
        raise HTTPException(status_code=400, detail="Invalid Stripe webhook.") from exc

    if not await _first_time(session, "stripe", event.id, event.type):
        return {"received": True, "duplicate": True}
    obj = event.data.object
    kind = event.type
    try:
        if kind in {
            "checkout.session.completed",
            "checkout.session.async_payment_succeeded",
        }:
            await events.fulfil_checkout(session, obj)
        elif kind == "checkout.session.expired":
            await events.close_checkout(session, obj, "expired")
        elif kind == "checkout.session.async_payment_failed":
            await events.close_checkout(session, obj, "failed")
        elif kind == "invoice.paid":
            await events.invoice_paid(session, obj, client)
        elif kind == "invoice.payment_failed":
            await events.invoice_failed(session, obj)
        elif kind == "customer.subscription.updated":
            await events.subscription_updated(session, obj)
        elif kind == "customer.subscription.deleted":
            await events.subscription_deleted(session, obj)
        await session.commit()
    except Exception:
        await session.rollback()
        logger.exception(
            "Stripe event %s (%s) failed; Stripe will retry", event.id, kind
        )
        raise
    return {"received": True}


@router.post("/crypto/webhook")
async def crypto_webhook(
    request: Request, session: AsyncSession = Depends(get_async_session)
):
    settings = await load_settings(session)
    gateway = crypto_provider(settings)
    body = await request.body()
    try:
        update = gateway.verify_webhook(body, dict(request.headers))
    except crypto_p.WebhookRejected as exc:
        logger.warning("Rejected %s callback: %s", gateway.name, exc)
        raise HTTPException(status_code=400, detail="Invalid callback.") from exc
    if not await _first_time(
        session, gateway.name, update.event_id or update.provider_ref, update.raw_status
    ):
        return PlainTextResponse("ok")
    # The signed event says something changed; BTCPay's own invoice record
    # says what (the event could arrive late or out of order).
    try:
        confirmed = await gateway.poll(update.provider_ref)
        if confirmed.order_id or not update.order_id:
            update = confirmed
    except crypto_p.ProviderUnavailable:
        logger.info(
            "%s invoice %s: using the webhook's status",
            gateway.name,
            update.provider_ref,
        )
    try:
        await apply_update(
            session,
            gateway,
            update,
        )
        await session.commit()
    except Exception:
        await session.rollback()
        logger.exception("%s callback for %s failed", gateway.name, update.provider_ref)
        raise
    return PlainTextResponse("ok")
