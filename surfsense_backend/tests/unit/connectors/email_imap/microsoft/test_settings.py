"""Whether this server can connect Microsoft mailboxes, from its environment."""

from __future__ import annotations

import pytest

from app.connectors.email_imap.microsoft.settings import load_microsoft_mail_settings

pytestmark = pytest.mark.unit

ENV = {
    "MICROSOFT_MAIL_CLIENT_ID": "11111111-2222-3333-4444-555555555555",
    "MICROSOFT_MAIL_CLIENT_SECRET": "a-client-secret-value",
}


def _load(env, **kwargs):
    defaults = {
        "backend_url": "https://notes.example.com/",
        "frontend_url": "https://notes.example.com",
        "secret_key": "test-secret-key-that-is-32-bytes-or-more",
    }
    return load_microsoft_mail_settings(env, **{**defaults, **kwargs})


def test_without_a_client_id_and_secret_it_is_off_and_names_what_is_missing():
    settings = _load({})
    assert not settings.configured
    assert settings.missing() == [
        "MICROSOFT_MAIL_CLIENT_ID",
        "MICROSOFT_MAIL_CLIENT_SECRET",
    ]


def test_with_both_it_is_on_for_every_kind_of_account_by_default():
    settings = _load(ENV)
    assert settings.configured
    assert settings.authorize_url == (
        "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"
    )
    assert settings.token_url == (
        "https://login.microsoftonline.com/common/oauth2/v2.0/token"
    )


def test_the_redirect_uri_is_the_public_api_callback_unless_set():
    assert _load(ENV).redirect_uri == (
        "https://notes.example.com/api/v1/auth/microsoft/mail/connector/callback"
    )
    custom = {**ENV, "MICROSOFT_MAIL_REDIRECT_URI": "https://x.example/cb"}
    assert _load(custom).redirect_uri == "https://x.example/cb"


def test_the_cookie_rides_only_with_the_callback_and_only_over_https():
    settings = _load(ENV)
    assert settings.cookie_path == "/api/v1/auth/microsoft/mail/connector/callback"
    assert settings.cookie_secure
    assert not _load(ENV, backend_url="http://localhost:8000").cookie_secure


def test_a_tenant_that_is_not_a_name_or_id_is_refused():
    assert _load({**ENV, "MICROSOFT_MAIL_TENANT": "organizations"}).configured
    bad = _load({**ENV, "MICROSOFT_MAIL_TENANT": "common/../evil"})
    assert bad.missing() == ["MICROSOFT_MAIL_TENANT"]


def test_the_secret_never_shows_in_repr():
    assert "a-client-secret-value" not in repr(_load(ENV))
