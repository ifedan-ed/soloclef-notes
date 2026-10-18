"""BILLING_ADMIN_EMAILS names an administrator only through a verified account.

An address in the list used to make whichever account held it an
administrator, verified or not: an account made with the password sign-up
form is never verified (no mail confirms it), so someone who signed up with
the administrator's address first held the admin pages.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from app.billing import ledger

pytestmark = pytest.mark.unit


@pytest.fixture(autouse=True)
def _admin_list(monkeypatch):
    monkeypatch.setattr(
        "app.config.config.BILLING_ADMIN_EMAILS", "Admin@Example.org, other@x.org"
    )


def _user(email, *, verified, superuser=False):
    return SimpleNamespace(email=email, is_verified=verified, is_superuser=superuser)


def test_a_listed_address_on_an_unverified_account_is_not_an_administrator():
    assert not ledger.is_config_admin(_user("admin@example.org", verified=False))
    assert not ledger.is_listed_admin("admin@example.org", verified=False)


def test_a_listed_address_on_a_verified_account_is_one_whatever_its_case():
    assert ledger.is_config_admin(_user("ADMIN@example.org", verified=True))
    assert ledger.is_listed_admin("admin@example.org", verified=True)


def test_an_unlisted_address_is_not_one_and_a_superuser_always_is():
    assert not ledger.is_config_admin(_user("someone@example.org", verified=True))
    assert ledger.is_config_admin(
        _user("someone@example.org", verified=False, superuser=True)
    )
    assert not ledger.is_config_admin(None)
