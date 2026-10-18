"""What OIDC_* in docker/.env turn into."""

from __future__ import annotations

import pytest

from app.auth_oidc.settings import (
    DEFAULT_LOGIN_ATTEMPTS_PER_MINUTE,
    load_oidc_settings,
    parse_domains,
)

pytestmark = pytest.mark.unit

FULL = {
    "OIDC_ENABLED": "TRUE",
    "OIDC_DISCOVERY_URL": "https://sso.example/application/o/notes/.well-known/openid-configuration",
    "OIDC_CLIENT_ID": "soloclef-notes",
    "OIDC_CLIENT_SECRET": "s3cret-value",
}


def test_off_unless_enabled():
    assert load_oidc_settings({}).enabled is False
    assert load_oidc_settings({"OIDC_ENABLED": "FALSE"}).enabled is False


def test_signup_is_off_by_default():
    settings = load_oidc_settings(FULL, backend_url="https://notes.example")
    assert settings.allow_signup is False
    assert load_oidc_settings({**FULL, "OIDC_ALLOW_SIGNUP": "TRUE"}).allow_signup


def test_no_domain_makes_accounts_by_default():
    assert load_oidc_settings(FULL).autocreate_domains == frozenset()


def test_autocreate_domains_are_read_from_the_env():
    settings = load_oidc_settings({**FULL, "OIDC_AUTOCREATE_DOMAINS": "example.com"})
    assert settings.autocreate_domains == frozenset({"example.com"})
    assert settings.allow_signup is False


@pytest.mark.parametrize(
    ("value", "expected"),
    [
        ("example.com", {"example.com"}),
        ("example.com,example.org", {"example.com", "example.org"}),
        (" example.com , example.org ", {"example.com", "example.org"}),
        ("example.com example.org", {"example.com", "example.org"}),
        ("@Example.COM", {"example.com"}),
        ("example.com.", {"example.com"}),
        ("", set()),
        (None, set()),
        (" , ,@ ", set()),
    ],
)
def test_domain_list_parsing(value, expected):
    assert parse_domains(value) == frozenset(expected)


def test_callback_is_on_the_public_backend_url():
    settings = load_oidc_settings(
        FULL,
        backend_url="https://notes.example/",
        frontend_url="https://notes.example/",
    )
    assert settings.redirect_uri == "https://notes.example/auth/oidc/callback"
    assert settings.frontend_url == "https://notes.example"
    assert settings.missing() == []


def test_names_every_missing_setting():
    settings = load_oidc_settings({"OIDC_ENABLED": "TRUE"})
    assert settings.missing() == [
        "OIDC_DISCOVERY_URL",
        "OIDC_CLIENT_ID",
        "OIDC_CLIENT_SECRET",
        "BACKEND_URL or SURFSENSE_PUBLIC_URL",
        "NEXT_FRONTEND_URL or SURFSENSE_PUBLIC_URL",
    ]


def test_openid_scope_is_always_asked_for():
    settings = load_oidc_settings({**FULL, "OIDC_SCOPES": "email profile"})
    assert settings.scopes == ("openid", "email", "profile")


def test_login_attempt_limit_defaults_and_overrides():
    assert (
        load_oidc_settings(FULL).login_attempts_per_minute
        == DEFAULT_LOGIN_ATTEMPTS_PER_MINUTE
    )
    raised = load_oidc_settings({**FULL, "OIDC_LOGIN_ATTEMPTS_PER_MINUTE": "100"})
    assert raised.login_attempts_per_minute == 100
    junk = load_oidc_settings({**FULL, "OIDC_LOGIN_ATTEMPTS_PER_MINUTE": "lots"})
    assert junk.login_attempts_per_minute == DEFAULT_LOGIN_ATTEMPTS_PER_MINUTE


def test_the_client_secret_never_appears_in_its_repr():
    assert "s3cret-value" not in repr(load_oidc_settings(FULL))
