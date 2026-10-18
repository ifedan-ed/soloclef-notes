"""BTCPay Server (Greenfield API): Bitcoin and USDT on Tron and Polygon.

Self-hosted at the operator's own BTCPay (``btcpay_url``, e.g.
https://btcpay.example.com); funds go to the wallets connected to that store.
API, read 2026-09-25 (https://docs.btcpayserver.org/API/Greenfield/v1/):

* ``POST /api/v1/stores/{storeId}/invoices`` with ``Authorization: token <key>``,
  body ``{amount, currency: "USD", metadata: {orderId}, checkout:
  {paymentMethods, redirectURL, expirationMinutes}}`` returns ``id``,
  ``checkoutLink``, ``status``, ``expirationTime``. The buyer picks Bitcoin or
  USDT on BTCPay's checkout page; ``checkout.paymentMethods`` limits the
  choice to ``BTC-CHAIN``, ``USDT-TRON``, ``USDT-POLYGON`` (the Tether "USDt"
  plugin's ids, compared case-insensitively).
* ``GET /api/v1/stores/{storeId}/invoices/{invoiceId}``: ``status`` New,
  Processing, Settled, Expired, Invalid; ``additionalStatus`` None, PaidLate,
  PaidPartial, Marked, Invalid, PaidOver. Used by the reconciliation poll and
  to confirm every webhook against BTCPay itself.
* Webhooks (https://docs.btcpayserver.org/API/Greenfield/v1/#tag/Webhooks):
  header ``BTCPay-Sig: sha256=<hex HMAC-SHA256 of the raw body, keyed with the
  webhook secret>``; types InvoiceCreated, InvoiceReceivedPayment,
  InvoicePaymentSettled, InvoiceProcessing, InvoiceExpired (``partiallyPaid``),
  InvoiceSettled (``overPaid``), InvoiceInvalid. A redelivery has a new
  ``deliveryId`` and the first one in ``originalDeliveryId``.

BTCPay decides when an invoice is paid (its store payment tolerance and speed
policy of 2/6/12/20 confirmations), so the amount check is BTCPay's; partial
payments stay open on BTCPay's own checkout page, which shows what is left.
"""

from __future__ import annotations

import hashlib
import hmac
import json
from datetime import UTC, datetime
from decimal import Decimal
from typing import Any

import httpx

from . import provider as p

DEFAULT_METHODS = ["BTC-CHAIN", "USDT-TRON", "USDT-POLYGON"]


class CryptoBusyError(p.ProviderUnavailable):
    """BTCPay could not give the invoice an address (every one is reserved)."""


def sign(body: bytes, secret: str) -> str:
    return "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()


def _invoice_status(status: str, additional: str | None) -> str:
    status = (status or "").lower()
    additional = (additional or "").lower()
    if status == "settled":
        return p.PAID
    if status == "processing":
        return p.CONFIRMING
    if status == "expired":
        return p.PARTIAL if additional == "paidpartial" else p.EXPIRED
    if status == "invalid":
        return p.FAILED
    return p.PENDING


_EVENTS = {
    "invoicecreated": p.PENDING,
    "invoicereceivedpayment": p.CONFIRMING,
    "invoicepaymentsettled": p.CONFIRMING,
    "invoiceprocessing": p.CONFIRMING,
    "invoicesettled": p.PAID,
    "invoiceinvalid": p.FAILED,
}


class BTCPay:
    name = "btcpay"

    def __init__(
        self,
        *,
        url: str | None,
        store_id: str | None,
        api_key: str | None,
        webhook_secret: str | None,
        payment_methods: list[str] | None = None,
        expiration_minutes: int = 60,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self.url = (url or "").rstrip("/")
        self.store_id = store_id or ""
        self._api_key = api_key
        self._secret = webhook_secret
        self.methods = [m.upper() for m in (payment_methods or DEFAULT_METHODS)]
        self._expiration = max(15, int(expiration_minutes))
        self._transport = transport

    def _client(self) -> httpx.AsyncClient:
        if not (self.url and self.store_id and self._api_key):
            raise p.ProviderUnavailable("BTCPay URL, store id or API key is not set")
        return httpx.AsyncClient(
            base_url=f"{self.url}/api/v1/stores/{self.store_id}",
            headers={"Authorization": f"token {self._api_key}"},
            timeout=20.0,
            transport=self._transport,
        )

    async def create_payment(
        self,
        *,
        order_id: str,
        price_usd: Decimal,
        pay_currency: str | None,
        description: str,
        callback_url: str,
        redirect_url: str | None = None,
    ) -> p.NewPayment:
        methods = self.methods
        if pay_currency and pay_currency.upper() in self.methods:
            methods = [pay_currency.upper()]
        payload: dict[str, Any] = {
            "amount": f"{price_usd:.2f}",
            "currency": "USD",
            "metadata": {"orderId": order_id, "itemDesc": description[:200]},
            "checkout": {
                "paymentMethods": methods,
                "expirationMinutes": self._expiration,
            },
        }
        if redirect_url:
            payload["checkout"]["redirectURL"] = redirect_url
        try:
            async with self._client() as client:
                response = await client.post("/invoices", json=payload)
        except httpx.HTTPError as exc:
            raise p.ProviderUnavailable(str(exc)) from exc
        if response.status_code >= 400:
            text = response.text.lower()
            if "address" in text or "no available" in text or "reserve" in text:
                raise CryptoBusyError(f"BTCPay {response.status_code}")
            raise p.ProviderUnavailable(f"BTCPay {response.status_code}")
        data = response.json()
        expires = data.get("expirationTime")
        return p.NewPayment(
            provider_ref=str(data["id"]),
            pay_address="",
            pay_amount=Decimal(str(data.get("amount") or price_usd)),
            pay_currency="USD",
            network=None,
            expires_at=datetime.fromtimestamp(int(expires), UTC) if expires else None,
            raw_status=str(data.get("status") or "New"),
            extra={"payment_url": data.get("checkoutLink")},
        )

    def verify_webhook(self, body: bytes, headers: dict[str, str]) -> p.PaymentUpdate:
        if not self._secret:
            raise p.WebhookRejected("webhook secret is not set")
        given = {k.lower(): v for k, v in headers.items()}.get("btcpay-sig", "")
        if not given or not hmac.compare_digest(
            sign(body, self._secret), given.strip().lower()
        ):
            raise p.WebhookRejected("bad signature")
        try:
            data = json.loads(body)
        except ValueError as exc:
            raise p.WebhookRejected("not JSON") from exc
        if not isinstance(data, dict) or not data.get("invoiceId"):
            raise p.WebhookRejected("not an invoice event")
        if data.get("storeId") and self.store_id and data["storeId"] != self.store_id:
            raise p.WebhookRejected("another store")
        kind = str(data.get("type") or "").lower()
        if kind == "invoiceexpired":
            raw = "expired_partial" if data.get("partiallyPaid") else "expired"
        else:
            raw = kind
        metadata = data.get("metadata") or {}
        return p.PaymentUpdate(
            provider_ref=str(data["invoiceId"]),
            raw_status=raw,
            pay_amount=None,
            actually_paid=None,
            pay_currency=(data.get("paymentMethodId") or data.get("paymentMethod")),
            price_amount=None,
            order_id=str(metadata["orderId"]) if metadata.get("orderId") else None,
            event_id=str(
                data.get("originalDeliveryId")
                or data.get("deliveryId")
                or f"{data['invoiceId']}:{kind}"
            ),
            extra={"over_paid": bool(data.get("overPaid"))},
        )

    async def poll(self, provider_ref: str) -> p.PaymentUpdate:
        try:
            async with self._client() as client:
                response = await client.get(f"/invoices/{provider_ref}")
        except httpx.HTTPError as exc:
            raise p.ProviderUnavailable(str(exc)) from exc
        if response.status_code >= 400:
            raise p.ProviderUnavailable(f"BTCPay {response.status_code}")
        data = response.json()
        metadata = data.get("metadata") or {}
        return p.PaymentUpdate(
            provider_ref=str(data.get("id") or provider_ref),
            raw_status=f"invoice:{data.get('status')}:{data.get('additionalStatus') or 'None'}",
            pay_amount=None,
            actually_paid=None,
            pay_currency=None,
            price_amount=Decimal(str(data["amount"])) if data.get("amount") else None,
            order_id=str(metadata["orderId"]) if metadata.get("orderId") else None,
            event_id=None,
            extra={
                "over_paid": (data.get("additionalStatus") or "").lower() == "paidover"
            },
        )

    def status(self, update: p.PaymentUpdate) -> str:
        raw = update.raw_status
        if raw.startswith("invoice:"):
            _prefix, status, additional = [*raw.split(":"), "", ""][:3]
            return _invoice_status(status, additional)
        if raw == "expired_partial":
            return p.PARTIAL
        if raw == "expired":
            return p.EXPIRED
        return _EVENTS.get(raw, p.PENDING)
