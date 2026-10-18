"""The billing settings that name a server (its BTCPay, its support address)
come from that server's configuration; the defaults name nobody's."""

from __future__ import annotations

import pytest

from app.billing import payment_methods
from app.billing.defaults import SETTINGS
from app.billing.persistence.models import BillingSetting
from app.billing.settings_store import BillingSettings
from app.support_contact import DEFAULT_SUPPORT_EMAIL, published_support_email

pytestmark = pytest.mark.unit


@pytest.fixture(autouse=True)
def _no_instance_env(monkeypatch: pytest.MonkeyPatch) -> None:
    for name in ("BTCPAY_URL", "SUPPORT_EMAIL", "BTCPAY_STORE_ID"):
        monkeypatch.delenv(name, raising=False)


def _stored(**values: object) -> BillingSettings:
    return BillingSettings(
        {key: BillingSetting(key=key, value=value) for key, value in values.items()}
    )


def test_the_seeded_defaults_name_no_server() -> None:
    assert SETTINGS["btcpay_url"] == ""
    assert SETTINGS["support_email"] == ""
    assert DEFAULT_SUPPORT_EMAIL == ""


def test_a_new_server_publishes_no_support_address() -> None:
    assert published_support_email(_stored()) == ""


def test_support_email_comes_from_the_environment(monkeypatch) -> None:
    monkeypatch.setenv("SUPPORT_EMAIL", " help@example.org ")
    assert published_support_email(_stored()) == "help@example.org"
    assert _stored().describe()["values"]["support_email"] == " help@example.org "


def test_an_address_the_admin_saved_wins_over_the_environment(monkeypatch) -> None:
    monkeypatch.setenv("SUPPORT_EMAIL", "env@example.org")
    assert published_support_email(_stored(support_email="admin@example.org")) == (
        "admin@example.org"
    )


@pytest.mark.parametrize("bad", ["not-an-address", "a@b", "x@y.z, evil@z.io"])
def test_an_unusable_address_publishes_none(monkeypatch, bad) -> None:
    monkeypatch.setenv("SUPPORT_EMAIL", bad)
    assert published_support_email(_stored()) == ""


def test_btcpay_url_comes_from_the_environment(monkeypatch) -> None:
    assert _stored().get("btcpay_url") == ""
    monkeypatch.setenv("BTCPAY_URL", "https://btcpay.example.org")
    assert _stored().get("btcpay_url") == "https://btcpay.example.org"
    assert _stored(btcpay_url="https://pay.example.net").get("btcpay_url") == (
        "https://pay.example.net"
    )


def test_crypto_without_a_server_is_never_offered() -> None:
    status = payment_methods.method_status(_stored(crypto_enabled=True), "crypto")
    assert not status.ready
    assert "btcpay_url" in status.missing
