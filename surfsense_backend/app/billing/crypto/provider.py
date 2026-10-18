"""The contract a crypto gateway implements."""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from decimal import Decimal
from typing import Any, Protocol

# Our statuses, whatever the provider calls them.
PENDING = "pending"  # created, nothing (confirmed) received yet
CONFIRMING = "confirming"  # seen on chain, not yet final
PARTIAL = "partial"  # final, but less than the price
PAID = "paid"  # final and enough: fulfil
FAILED = "failed"
EXPIRED = "expired"
REFUNDED = "refunded"
FINAL = frozenset({PAID, FAILED, EXPIRED, REFUNDED})


@dataclass(frozen=True)
class NewPayment:
    """What the customer must send."""

    provider_ref: str
    pay_address: str
    pay_amount: Decimal
    pay_currency: str
    network: str | None
    expires_at: datetime | None
    raw_status: str
    extra: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True)
class PaymentUpdate:
    """A provider's view of one payment, from a webhook or a poll."""

    provider_ref: str
    raw_status: str
    pay_amount: Decimal | None
    actually_paid: Decimal | None
    pay_currency: str | None
    price_amount: Decimal | None
    order_id: str | None
    outcome_amount: Decimal | None = None
    event_id: str | None = None
    extra: dict[str, Any] = field(default_factory=dict)


class CryptoProvider(Protocol):
    name: str

    async def create_payment(
        self,
        *,
        order_id: str,
        price_usd: Decimal,
        pay_currency: str | None,
        description: str,
        callback_url: str,
        redirect_url: str | None = None,
    ) -> NewPayment: ...

    def verify_webhook(self, body: bytes, headers: dict[str, str]) -> PaymentUpdate:
        """Parse a callback, raising ``WebhookRejected`` if it is not authentic."""
        ...

    async def poll(self, provider_ref: str) -> PaymentUpdate: ...

    def status(self, update: PaymentUpdate) -> str:
        """Map to one of our statuses (before the amount check)."""
        ...


class WebhookRejected(Exception):  # noqa: N818
    """A callback whose signature or shape does not check out."""


class ProviderUnavailable(Exception):  # noqa: N818
    """The provider could not be reached or refused the request."""
