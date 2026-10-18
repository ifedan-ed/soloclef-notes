"""Which payment methods people can use right now.

A method is offered only when it is switched on in Billing -> Admin -> Mode &
payments *and* everything it needs to take and confirm a payment is set: for
card (Stripe) the secret key and the webhook signing secret, for crypto
(BTCPay) the server URL, store id, API key and webhook secret. Anything short
of that is "pending": the admin page says what is missing, and people see
neither the method nor any copy promising it.
"""

from __future__ import annotations

from dataclasses import dataclass

from .settings_store import BillingSettings

CARD = "card"
CRYPTO = "crypto"

# What each method needs besides its on-switch, in the order the admin page
# lists what is missing.
_REQUIRED: dict[str, tuple[str, ...]] = {
    CARD: ("stripe_secret_key", "stripe_webhook_secret"),
    CRYPTO: (
        "btcpay_url",
        "btcpay_store_id",
        "btcpay_api_key",
        "btcpay_webhook_secret",
    ),
}
_SWITCH = {CARD: "stripe_enabled", CRYPTO: "crypto_enabled"}


@dataclass(frozen=True)
class MethodStatus:
    enabled: bool
    missing: tuple[str, ...]

    @property
    def ready(self) -> bool:
        return self.enabled and not self.missing

    def describe(self) -> dict:
        return {
            "ready": self.ready,
            "enabled": self.enabled,
            "missing": list(self.missing),
        }


def method_status(settings: BillingSettings, method: str) -> MethodStatus:
    missing = tuple(
        key for key in _REQUIRED[method] if not str(settings.get(key) or "").strip()
    )
    return MethodStatus(enabled=bool(settings.get(_SWITCH[method])), missing=missing)


def card_ready(settings: BillingSettings) -> bool:
    return method_status(settings, CARD).ready


def crypto_ready(settings: BillingSettings) -> bool:
    return method_status(settings, CRYPTO).ready


def statuses(settings: BillingSettings) -> dict[str, dict]:
    """Both methods for the admin page: ready, switched on, and what is missing."""
    return {m: method_status(settings, m).describe() for m in (CARD, CRYPTO)}
