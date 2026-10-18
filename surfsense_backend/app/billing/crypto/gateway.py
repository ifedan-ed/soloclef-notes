"""The configured crypto provider, built from billing settings."""

from __future__ import annotations

from .. import payment_methods
from ..settings_store import BillingSettings
from .btcpay import BTCPay


def crypto_provider(settings: BillingSettings) -> BTCPay:
    return BTCPay(
        url=settings.get("btcpay_url"),
        store_id=settings.get("btcpay_store_id"),
        api_key=settings.get("btcpay_api_key"),
        webhook_secret=settings.get("btcpay_webhook_secret"),
        payment_methods=list(settings.get("crypto_payment_methods") or []) or None,
    )


def crypto_ready(settings: BillingSettings) -> bool:
    """Switched on, and the URL, store id, API key and webhook secret are set."""
    return payment_methods.crypto_ready(settings)
