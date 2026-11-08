"""Which addresses a first single sign-on may make a Notes account for."""

from __future__ import annotations

import pytest

from app.auth_oidc.account_creation_rule import (
    describe,
    email_domain,
    may_create_account,
)

pytestmark = pytest.mark.unit

DANVICS = frozenset({"danvics.com"})


def _may(email: str, *, allow_signup: bool = False, domains=DANVICS) -> bool:
    return may_create_account(
        email, allow_signup=allow_signup, autocreate_domains=domains
    )


def test_an_address_at_a_listed_domain_may():
    assert _may("ifee@danvics.com")


def test_the_domain_is_compared_without_case():
    assert _may("Ifee@DanVics.COM")


@pytest.mark.parametrize(
    "email",
    [
        "friend@gmail.com",
        "someone@sub.danvics.com",  # a subdomain is another domain
        "someone@evil-danvics.com",
        "someone@danvics.com.example",
        "someone@xdanvics.com",
        "danvics.com@gmail.com",
    ],
)
def test_any_other_domain_may_not(email):
    assert not _may(email)


@pytest.mark.parametrize(
    "email", ["", "danvics.com", "@danvics.com", "a@b@danvics.com", "   "]
)
def test_a_malformed_address_never_may(email):
    assert not _may(email)
    assert not _may(email, allow_signup=True)


def test_an_empty_list_with_signup_off_makes_no_accounts():
    assert not _may("ifee@danvics.com", domains=frozenset())


def test_signup_on_opens_every_domain():
    assert _may("friend@gmail.com", allow_signup=True)
    assert _may("friend@gmail.com", allow_signup=True, domains=frozenset())


def test_email_domain_is_the_part_after_the_at_sign():
    assert email_domain(" Someone@Danvics.com ") == "danvics.com"
    assert email_domain("no-at-sign") == ""


def test_the_rule_reads_plainly_in_the_log():
    assert describe(allow_signup=False, autocreate_domains=frozenset()) == (
        "nobody (existing accounts only)"
    )
    assert (
        describe(allow_signup=False, autocreate_domains=DANVICS)
        == "verified addresses at danvics.com"
    )
    assert (
        describe(allow_signup=True, autocreate_domains=DANVICS)
        == "any verified address"
    )
