"""The Stripe client, built from billing settings."""

from __future__ import annotations

import os

from fastapi import HTTPException, status
from stripe import StripeClient

from .. import payment_methods
from ..settings_store import BillingSettings


def stripe_client(settings: BillingSettings) -> StripeClient:
    key = settings.get("stripe_secret_key")
    if not key:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail={
                "code": "STRIPE_NOT_CONFIGURED",
                "message": "Card payments are not set up yet.",
            },
        )
    # STRIPE_API_BASE points tests at stripe-mock; unset in production.
    base = os.getenv("STRIPE_API_BASE")
    if base:
        return StripeClient(key, base_addresses={"api": base}, max_network_retries=0)
    return StripeClient(key, max_network_retries=2)


def stripe_ready(settings: BillingSettings) -> bool:
    """Switched on, and the secret key and webhook signing secret are set."""
    return payment_methods.card_ready(settings)
