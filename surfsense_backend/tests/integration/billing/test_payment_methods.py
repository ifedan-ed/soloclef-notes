"""Only payment methods that are switched on and fully set up are offered.

Crypto-only is how Soloclef Notes starts (Stripe pending): card buttons, the
portal and card copy must not appear, and until BTCPay is finished (store id,
API key, webhook secret, switch) nothing is offered at all.
"""

from __future__ import annotations

import pytest

from app.billing.settings_store import save_settings

pytestmark = pytest.mark.integration

_ENV = (
    "STRIPE_SECRET_KEY",
    "STRIPE_WEBHOOK_SECRET",
    "BTCPAY_API_KEY",
    "BTCPAY_WEBHOOK_SECRET",
    "BTCPAY_URL",
    "BTCPAY_STORE_ID",
)


@pytest.fixture(autouse=True)
def _no_payment_env(monkeypatch):
    for name in _ENV:
        monkeypatch.delenv(name, raising=False)


@pytest.fixture
def configure(db_session):
    async def apply(*, values=None, secrets=None):
        await save_settings(db_session, values=values or {}, secrets=secrets or {})

    return apply


CRYPTO_VALUES = {
    "crypto_enabled": True,
    "btcpay_url": "https://pay.example",
    "btcpay_store_id": "Store1",
}
CRYPTO_SECRETS = {"btcpay_api_key": "key", "btcpay_webhook_secret": "whsec"}


async def _me(api, user):
    async with api(user) as client:
        return (await client.get("/api/v1/billing/me")).json()


async def _site(api):
    async with api(None) as client:
        return (await client.get("/api/v1/billing/public/catalog")).json()


async def test_nothing_is_offered_until_a_method_is_set_up(db_user, api, set_mode):
    await set_mode("live")
    me = await _me(api, db_user)
    assert me["payments"]["card"] is False
    assert me["payments"]["crypto"]["enabled"] is False
    site = await _site(api)
    assert site["payments"] == {
        "card": False,
        "crypto": False,
        "cryptoMonthOptions": [],
    }


async def test_crypto_needs_its_webhook_secret_too(db_user, api, set_mode, configure):
    await set_mode("live")
    await configure(values=CRYPTO_VALUES, secrets={"btcpay_api_key": "key"})
    assert (await _me(api, db_user))["payments"]["crypto"]["enabled"] is False

    await configure(secrets={"btcpay_webhook_secret": "whsec"})
    assert (await _me(api, db_user))["payments"]["crypto"]["enabled"] is True


async def test_a_configured_crypto_is_hidden_while_switched_off(
    db_user, api, set_mode, configure
):
    await set_mode("live")
    await configure(
        values={**CRYPTO_VALUES, "crypto_enabled": False}, secrets=CRYPTO_SECRETS
    )
    assert (await _me(api, db_user))["payments"]["crypto"]["enabled"] is False


async def test_crypto_only_offers_no_card(db_user, api, set_mode, configure):
    await set_mode("live")
    # Stripe switched on without its key stays pending.
    await configure(
        values={**CRYPTO_VALUES, "stripe_enabled": True}, secrets=CRYPTO_SECRETS
    )
    me = await _me(api, db_user)
    assert me["payments"]["card"] is False
    assert me["payments"]["crypto"]["enabled"] is True
    assert me["payments"]["crypto"]["month_options"] == [1, 3, 12]
    site = await _site(api)
    assert site["payments"] == {
        "card": False,
        "crypto": True,
        "cryptoMonthOptions": [1, 3, 12],
    }
    async with api(db_user) as client:
        portal = await client.post("/api/v1/billing/stripe/portal")
        checkout = await client.post(
            "/api/v1/billing/stripe/checkout", json={"kind": "pack", "pack_id": 1}
        )
    assert portal.status_code == 503 and checkout.status_code == 503
    assert portal.json()["detail"]["code"] == "CARD_PAYMENTS_OFF"


async def test_card_needs_key_and_webhook_secret(db_user, api, set_mode, configure):
    await set_mode("live")
    await configure(
        values={"stripe_enabled": True}, secrets={"stripe_secret_key": "sk_test_1"}
    )
    assert (await _me(api, db_user))["payments"]["card"] is False
    await configure(secrets={"stripe_webhook_secret": "whsec_1"})
    assert (await _me(api, db_user))["payments"]["card"] is True
    assert (await _site(api))["payments"]["card"] is True


async def test_the_admin_sees_each_method_and_what_it_lacks(
    new_user, api, set_mode, configure
):
    admin = await new_user("owner@surfsense.net")
    await set_mode("live")
    await configure(values=CRYPTO_VALUES, secrets={"btcpay_api_key": "key"})
    async with api(admin) as client:
        body = (await client.get("/api/v1/billing/admin/overview")).json()
    payments = body["settings"]["payments"]
    assert payments["card"] == {
        "ready": False,
        "enabled": False,
        "missing": ["stripe_secret_key", "stripe_webhook_secret"],
    }
    assert payments["crypto"] == {
        "ready": False,
        "enabled": True,
        "missing": ["btcpay_webhook_secret"],
    }
