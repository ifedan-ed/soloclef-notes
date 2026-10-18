"""Which account a verified identity signs in to (the rule, without a database)."""

from __future__ import annotations

import uuid

import pytest

from app.auth_oidc.account_link import Match, decide
from app.auth_oidc.errors import (
    ACCOUNT_NOT_VERIFIED,
    EMAIL_NOT_VERIFIED,
    NO_ACCOUNT,
    OidcLoginError,
)

pytestmark = pytest.mark.unit

SOMEONE = uuid.uuid4()
SOMEONE_ELSE = uuid.uuid4()


def test_a_known_identity_signs_in_to_its_account_whatever_its_email():
    assert (
        decide(
            linked_user_id=SOMEONE,
            email_user_id=SOMEONE_ELSE,
            email="someone@example.com",
            email_verified=False,
            allow_signup=False,
        )
        is Match.LINKED
    )


def test_a_first_sign_in_links_by_verified_email():
    assert (
        decide(
            linked_user_id=None,
            email_user_id=SOMEONE,
            email="someone@example.com",
            email_verified=True,
            allow_signup=False,
            email_user_verified=True,
        )
        is Match.BY_EMAIL
    )


def test_an_unverified_email_never_links():
    with pytest.raises(OidcLoginError) as refused:
        decide(
            linked_user_id=None,
            email_user_id=SOMEONE,
            email="someone@example.com",
            email_verified=False,
            allow_signup=True,
        )
    assert refused.value.code == EMAIL_NOT_VERIFIED


def test_no_account_and_signup_off_is_refused():
    with pytest.raises(OidcLoginError) as refused:
        decide(
            linked_user_id=None,
            email_user_id=None,
            email="someone@example.com",
            email_verified=True,
            allow_signup=False,
        )
    assert refused.value.code == NO_ACCOUNT


def test_no_account_and_signup_on_makes_one():
    assert (
        decide(
            linked_user_id=None,
            email_user_id=None,
            email="someone@example.com",
            email_verified=True,
            allow_signup=True,
        )
        is Match.NEW_ACCOUNT
    )


AUTOCREATE_DOMAINS = frozenset({"example.com"})


def test_an_address_at_an_autocreate_domain_gets_an_account_with_signup_off():
    assert (
        decide(
            linked_user_id=None,
            email_user_id=None,
            email="ifee@example.com",
            email_verified=True,
            allow_signup=False,
            autocreate_domains=AUTOCREATE_DOMAINS,
        )
        is Match.NEW_ACCOUNT
    )


def test_an_address_at_another_domain_still_needs_an_account():
    with pytest.raises(OidcLoginError) as refused:
        decide(
            linked_user_id=None,
            email_user_id=None,
            email="friend@gmail.com",
            email_verified=True,
            allow_signup=False,
            autocreate_domains=AUTOCREATE_DOMAINS,
        )
    assert refused.value.code == NO_ACCOUNT


def test_an_unverified_address_at_an_autocreate_domain_gets_nothing():
    with pytest.raises(OidcLoginError) as refused:
        decide(
            linked_user_id=None,
            email_user_id=None,
            email="ifee@example.com",
            email_verified=False,
            allow_signup=False,
            autocreate_domains=AUTOCREATE_DOMAINS,
        )
    assert refused.value.code == EMAIL_NOT_VERIFIED


def test_an_existing_account_on_any_domain_still_links_by_email():
    assert (
        decide(
            linked_user_id=None,
            email_user_id=SOMEONE,
            email="friend@gmail.com",
            email_verified=True,
            allow_signup=False,
            email_user_verified=True,
            autocreate_domains=AUTOCREATE_DOMAINS,
        )
        is Match.BY_EMAIL
    )


def test_an_existing_account_at_an_autocreate_domain_is_linked_not_duplicated():
    assert (
        decide(
            linked_user_id=None,
            email_user_id=SOMEONE,
            email="you@example.com",
            email_verified=True,
            allow_signup=False,
            email_user_verified=True,
            autocreate_domains=AUTOCREATE_DOMAINS,
        )
        is Match.BY_EMAIL
    )


def test_no_address_never_makes_an_account():
    with pytest.raises(OidcLoginError) as refused:
        decide(
            linked_user_id=None,
            email_user_id=None,
            email="",
            email_verified=True,
            allow_signup=True,
        )
    assert refused.value.code == NO_ACCOUNT


def test_an_account_whose_own_address_was_never_verified_is_not_linked():
    # Someone signed up with (or switched an account to) this address: the
    # address's real owner signing in by SSO must not land in that account.
    with pytest.raises(OidcLoginError) as refused:
        decide(
            linked_user_id=None,
            email_user_id=SOMEONE,
            email="someone@example.com",
            email_verified=True,
            allow_signup=True,
            email_user_verified=False,
        )
    assert refused.value.code == ACCOUNT_NOT_VERIFIED


def test_an_account_verified_or_not_never_matters_once_linked():
    assert (
        decide(
            linked_user_id=SOMEONE,
            email_user_id=SOMEONE,
            email="someone@example.com",
            email_verified=True,
            allow_signup=False,
            email_user_verified=False,
        )
        is Match.LINKED
    )
