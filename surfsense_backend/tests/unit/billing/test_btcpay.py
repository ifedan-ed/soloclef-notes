"""BTCPay webhooks: signature, event and invoice status mapping."""

from __future__ import annotations

import json

import pytest

from app.billing.crypto import provider as p
from app.billing.crypto.btcpay import BTCPay, sign

pytestmark = pytest.mark.unit

SECRET = "webhook-secret"


def _gw():
    return BTCPay(
        url="https://pay.example", store_id="store1", api_key="k", webhook_secret=SECRET
    )


def _body(**data):
    base = {
        "deliveryId": "d1",
        "webhookId": "w1",
        "originalDeliveryId": "d1",
        "isRedelivery": False,
        "type": "InvoiceSettled",
        "timestamp": 1,
        "storeId": "store1",
        "invoiceId": "inv1",
        "metadata": {"orderId": "sn-1"},
        "overPaid": False,
    }
    base.update(data)
    return json.dumps(base).encode()


def test_a_signed_event_is_accepted():
    body = _body()
    update = _gw().verify_webhook(body, {"BTCPay-Sig": sign(body, SECRET)})
    assert update.provider_ref == "inv1" and update.order_id == "sn-1"
    assert _gw().status(update) == p.PAID


@pytest.mark.parametrize(
    "headers",
    [{}, {"BTCPay-Sig": "sha256=" + "0" * 64}, {"BTCPay-Sig": sign(b"x", SECRET)}],
)
def test_unsigned_or_forged_events_are_rejected(headers):
    with pytest.raises(p.WebhookRejected):
        _gw().verify_webhook(_body(), headers)


def test_events_for_another_store_are_rejected():
    body = _body(storeId="someone-else")
    with pytest.raises(p.WebhookRejected):
        _gw().verify_webhook(body, {"btcpay-sig": sign(body, SECRET)})


def test_a_redelivery_keeps_the_original_event_id():
    first = _body()
    again = _body(deliveryId="d2", isRedelivery=True)
    gw = _gw()
    assert (
        gw.verify_webhook(first, {"BTCPay-Sig": sign(first, SECRET)}).event_id
        == gw.verify_webhook(again, {"BTCPay-Sig": sign(again, SECRET)}).event_id
    )


@pytest.mark.parametrize(
    ("kind", "extra", "ours"),
    [
        ("InvoiceCreated", {}, p.PENDING),
        ("InvoiceReceivedPayment", {}, p.CONFIRMING),
        ("InvoiceProcessing", {}, p.CONFIRMING),
        ("InvoiceSettled", {"overPaid": True}, p.PAID),
        ("InvoiceExpired", {"partiallyPaid": True}, p.PARTIAL),
        ("InvoiceExpired", {}, p.EXPIRED),
        ("InvoiceInvalid", {}, p.FAILED),
    ],
)
def test_events_map_to_ours(kind, extra, ours):
    body = _body(type=kind, **extra)
    assert (
        _gw().status(_gw().verify_webhook(body, {"BTCPay-Sig": sign(body, SECRET)}))
        == ours
    )


@pytest.mark.parametrize(
    ("status", "additional", "ours"),
    [
        ("New", "None", p.PENDING),
        ("Processing", "None", p.CONFIRMING),
        ("Settled", "PaidOver", p.PAID),
        ("Expired", "PaidPartial", p.PARTIAL),
        ("Expired", "None", p.EXPIRED),
        ("Invalid", "Invalid", p.FAILED),
    ],
)
def test_invoice_statuses_map_to_ours(status, additional, ours):
    update = p.PaymentUpdate(
        "inv1", f"invoice:{status}:{additional}", None, None, None, None, None
    )
    assert _gw().status(update) == ours
