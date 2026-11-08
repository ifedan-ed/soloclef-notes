"""Crypto through BTCPay, against a mocked Greenfield API (httpx MockTransport)."""

from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta

import httpx
import pytest
from dateutil.relativedelta import relativedelta
from sqlalchemy import select

from app.billing import ledger
from app.billing.crypto.btcpay import BTCPay, sign
from app.billing.meter import sibling_sessions
from app.billing.persistence.models import BillingPack, BillingPayment, BillingPlan
from app.billing.settings_store import load_settings, save_settings
from app.billing.tasks import reconcile_payments
from app.billing.units import credits_to_micros

pytestmark = pytest.mark.integration

KEY = "btcpay-api-key"
SECRET = "btcpay-webhook-secret"
STORE = "StoreAbc"


class MockGreenfield:
    """The Greenfield endpoints billing uses; invoices live in ``self.invoices``."""

    def __init__(self):
        self.created: list[dict] = []
        self.invoices: dict[str, dict] = {}
        self.busy = False

    def handler(self, request: httpx.Request) -> httpx.Response:
        assert request.headers["authorization"] == f"token {KEY}"
        assert request.url.path.startswith(f"/api/v1/stores/{STORE}/invoices")
        if request.method == "POST":
            if self.busy:
                return httpx.Response(
                    400, json=[{"message": "No available address for USDT-TRON"}]
                )
            body = json.loads(request.content)
            self.created.append(body)
            invoice_id = f"inv{len(self.created)}"
            self.invoices[invoice_id] = {
                "id": invoice_id,
                "status": "New",
                "additionalStatus": "None",
                "amount": body["amount"],
                "currency": body["currency"],
                "metadata": body["metadata"],
            }
            return httpx.Response(
                200,
                json={
                    **self.invoices[invoice_id],
                    "checkoutLink": f"https://pay.example/i/{invoice_id}",
                    "expirationTime": int(datetime.now(UTC).timestamp()) + 3600,
                },
            )
        invoice_id = request.url.path.rsplit("/", 1)[-1]
        if invoice_id not in self.invoices:
            return httpx.Response(404, json={"code": "invoice-not-found"})
        return httpx.Response(200, json=self.invoices[invoice_id])

    def set(self, invoice_id, status, additional="None"):
        self.invoices[invoice_id] |= {"status": status, "additionalStatus": additional}


@pytest.fixture
async def btcpay(db_session, set_mode, monkeypatch):
    mock = MockGreenfield()
    await set_mode("live")
    await save_settings(
        db_session,
        values={
            "crypto_enabled": True,
            "btcpay_url": "https://pay.example",
            "btcpay_store_id": STORE,
        },
        secrets={"btcpay_api_key": KEY, "btcpay_webhook_secret": SECRET},
    )

    def provider(settings):
        return BTCPay(
            url=settings.get("btcpay_url"),
            store_id=settings.get("btcpay_store_id"),
            api_key=settings.get("btcpay_api_key"),
            webhook_secret=settings.get("btcpay_webhook_secret"),
            payment_methods=settings.get("crypto_payment_methods"),
            transport=httpx.MockTransport(mock.handler),
        )

    for target in (
        "app.billing.routes.user.crypto_provider",
        "app.billing.routes.webhooks.crypto_provider",
    ):
        monkeypatch.setattr(target, provider)
    monkeypatch.setattr("app.config.config.BACKEND_URL", "https://notes.example")
    monkeypatch.setattr("app.config.config.NEXT_FRONTEND_URL", "https://notes.example")
    mock.provider = provider
    return mock


def event(invoice_id, kind, order_id, delivery, **extra):
    body = json.dumps(
        {
            "deliveryId": delivery,
            "originalDeliveryId": delivery,
            "type": kind,
            "storeId": STORE,
            "invoiceId": invoice_id,
            "metadata": {"orderId": order_id},
            **extra,
        }
    ).encode()
    return body, {"BTCPay-Sig": sign(body, SECRET), "Content-Type": "application/json"}


async def _buy_pack(api, user, db_session, **extra):
    pack = await db_session.scalar(select(BillingPack).order_by(BillingPack.sort_order))
    async with api(user) as client:
        response = await client.post(
            "/api/v1/billing/crypto/payments",
            json={"kind": "pack", "pack_id": pack.id, **extra},
        )
    return response, pack


async def _topup(db_session, user):
    _a, u = await ledger.lock_account(db_session, user.id)
    return u.credit_micros_balance


async def _order(db_session, payment_id):
    return (await db_session.get(BillingPayment, payment_id)).details["order_id"]


async def test_an_invoice_is_priced_by_the_server_and_opens_btcpays_checkout(
    db_session, db_user, btcpay, api
):
    response, _pack = await _buy_pack(api, db_user, db_session, amount_cents=1)
    assert response.status_code == 200, response.text
    body = response.json()
    assert body["crypto"]["payment_url"].startswith("https://pay.example/i/")
    sent = btcpay.created[-1]
    assert sent["amount"] == "5.00" and sent["currency"] == "USD"
    assert sent["checkout"]["paymentMethods"] == [
        "BTC-CHAIN",
        "USDT-TRON",
        "USDT-POLYGON",
    ]
    assert (
        sent["checkout"]["redirectURL"] == "https://notes.example/billing?crypto=done"
    )


async def test_one_method_can_be_chosen_and_unknown_ones_are_refused(
    db_session, db_user, btcpay, api
):
    response, _ = await _buy_pack(api, db_user, db_session, pay_currency="usdt-tron")
    assert response.status_code == 200
    assert btcpay.created[-1]["checkout"]["paymentMethods"] == ["USDT-TRON"]
    refused, _ = await _buy_pack(api, db_user, db_session, pay_currency="DOGE-CHAIN")
    assert refused.status_code == 400


async def test_no_free_address_says_crypto_is_busy(db_session, db_user, btcpay, api):
    btcpay.busy = True
    response, _ = await _buy_pack(api, db_user, db_session)
    assert response.status_code == 503
    assert response.json()["error"]["code"] == "CRYPTO_BUSY"


async def test_settled_grants_credits_once_despite_redeliveries(
    db_session, db_user, btcpay, api
):
    response, pack = await _buy_pack(api, db_user, db_session)
    payment = response.json()
    order = await _order(db_session, payment["id"])
    btcpay.set("inv1", "Settled")
    async with api(None) as client:
        for kind, delivery in [
            ("InvoiceReceivedPayment", "d1"),
            ("InvoiceSettled", "d2"),
            ("InvoiceSettled", "d2"),
            ("InvoicePaymentSettled", "d3"),
        ]:
            body, headers = event("inv1", kind, order, delivery)
            assert (
                await client.post(
                    "/api/v1/billing/crypto/webhook", content=body, headers=headers
                )
            ).status_code == 200
    assert await _topup(db_session, db_user) == pack.credits_micros
    assert (
        await db_session.get(BillingPayment, payment["id"], populate_existing=True)
    ).status == "paid"


async def test_a_forged_settled_event_is_refused_and_btcpay_is_asked(
    db_session, db_user, btcpay, api
):
    response, _ = await _buy_pack(api, db_user, db_session)
    order = await _order(db_session, response.json()["id"])
    async with api(None) as client:
        body, _h = event("inv1", "InvoiceSettled", order, "dx")
        forged = await client.post(
            "/api/v1/billing/crypto/webhook",
            content=body,
            headers={"BTCPay-Sig": sign(body, "guess")},
        )
        assert forged.status_code == 400
        # Correctly signed, but BTCPay itself still says New: nothing is granted.
        body, headers = event("inv1", "InvoiceSettled", order, "dy")
        await client.post(
            "/api/v1/billing/crypto/webhook", content=body, headers=headers
        )
    assert await _topup(db_session, db_user) == 0


async def test_partial_and_expired_invoices(db_session, db_user, btcpay, api):
    first, _ = await _buy_pack(api, db_user, db_session)
    second, _ = await _buy_pack(api, db_user, db_session)
    btcpay.set("inv1", "Expired", "PaidPartial")
    btcpay.set("inv2", "Expired")
    async with api(None) as client:
        for inv, pid in [("inv1", first.json()["id"]), ("inv2", second.json()["id"])]:
            body, headers = event(
                inv, "InvoiceExpired", await _order(db_session, pid), f"e-{inv}"
            )
            await client.post(
                "/api/v1/billing/crypto/webhook", content=body, headers=headers
            )
    assert (
        await db_session.get(BillingPayment, first.json()["id"], populate_existing=True)
    ).status == "partial"
    assert (
        await db_session.get(
            BillingPayment, second.json()["id"], populate_existing=True
        )
    ).status == "expired"
    assert await _topup(db_session, db_user) == 0


async def test_an_event_naming_another_order_is_ignored(
    db_session, db_user, btcpay, api
):
    response, _ = await _buy_pack(api, db_user, db_session)
    btcpay.invoices["inv1"]["metadata"] = {"orderId": "sn-someone-else"}
    btcpay.set("inv1", "Settled")
    async with api(None) as client:
        body, headers = event("inv1", "InvoiceSettled", "sn-someone-else", "z1")
        await client.post(
            "/api/v1/billing/crypto/webhook", content=body, headers=headers
        )
    assert (
        await db_session.get(
            BillingPayment, response.json()["id"], populate_existing=True
        )
    ).status == "pending"


async def test_prepaying_months_extends_the_plan_with_the_discount(
    db_session, db_user, btcpay, api
):
    plan = await db_session.scalar(
        select(BillingPlan).where(BillingPlan.slug == "plus")
    )
    async with api(db_user) as client:
        response = await client.post(
            "/api/v1/billing/crypto/payments",
            json={"kind": "plan", "plan_id": plan.id, "months": 12},
        )
    assert response.status_code == 200, response.text
    payment = response.json()
    assert payment["amount_cents"] == round(900 * 12 * 0.9)
    assert btcpay.created[-1]["amount"] == "97.20"
    btcpay.set("inv1", "Settled")
    async with api(None) as client:
        body, headers = event(
            "inv1", "InvoiceSettled", await _order(db_session, payment["id"]), "p1"
        )
        await client.post(
            "/api/v1/billing/crypto/webhook", content=body, headers=headers
        )
    account, _ = await ledger.lock_account(db_session, db_user.id)
    assert account.plan_id == plan.id and account.plan_source == "crypto"
    assert account.plan_paid_until > datetime.now(UTC) + relativedelta(months=11)
    assert account.plan_credits_micros == credits_to_micros(1500)
    async with api(db_user) as client:
        months = await client.post(
            "/api/v1/billing/crypto/payments",
            json={"kind": "plan", "plan_id": plan.id, "months": 5},
        )
    assert months.status_code == 400


async def test_a_missed_webhook_is_recovered_by_the_poll(
    db_session, db_user, btcpay, api
):
    response, pack = await _buy_pack(api, db_user, db_session)
    row = await db_session.get(BillingPayment, response.json()["id"])
    row.created_at = datetime.now(UTC) - timedelta(minutes=20)
    await db_session.flush()
    btcpay.set("inv1", "Settled")
    handled = await reconcile_payments(
        sibling_sessions(db_session),
        gateway=btcpay.provider(await load_settings(db_session)),
    )
    assert handled >= 1
    assert await _topup(db_session, db_user) == pack.credits_micros


async def test_nobody_can_read_another_persons_payment(
    db_session, db_user, make_user, btcpay, api
):
    response, _ = await _buy_pack(api, db_user, db_session)
    other = await make_user()
    async with api(other) as client:
        assert (
            await client.get(f"/api/v1/billing/crypto/payments/{response.json()['id']}")
        ).status_code == 404
