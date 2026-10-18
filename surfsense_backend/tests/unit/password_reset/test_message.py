"""The reset email: from the product, linking to the site, token out of the URL path."""

from __future__ import annotations

import pytest

from app.config import config
from app.password_reset.message import account_sender, build_reset_email, reset_link

pytestmark = pytest.mark.unit


@pytest.fixture(autouse=True)
def _site(monkeypatch):
    monkeypatch.setattr(config, "NEXT_FRONTEND_URL", "https://notes.example.com/")
    monkeypatch.setattr(config, "SMTP_FROM", "notes@example.com")
    monkeypatch.setattr(config, "SMTP_ACCOUNT_FROM", "")
    monkeypatch.setattr(config, "PASSWORD_RESET_TOKEN_LIFETIME_SECONDS", 1800)


def test_the_link_points_at_the_site_with_the_token_in_the_fragment():
    assert (
        reset_link("abc.def")
        == "https://notes.example.com/reset-password#token=abc.def"
    )


def test_the_sender_is_the_product_name_on_the_configured_address(monkeypatch):
    assert account_sender() == "Soloclef Notes <notes@example.com>"
    monkeypatch.setattr(config, "SMTP_ACCOUNT_FROM", "Accounts <accounts@example.com>")
    assert account_sender() == "Accounts <accounts@example.com>"


def test_the_email_names_the_account_link_and_expiry():
    mail = build_reset_email(to="someone@example.com", token="tok123")
    assert mail.to == "someone@example.com"
    assert mail.subject == "Reset your Soloclef Notes password"
    assert mail.sender == "Soloclef Notes <notes@example.com>"
    for body in (mail.text_body, mail.html_body):
        assert "https://notes.example.com/reset-password#token=tok123" in body
        assert "30 minutes" in body


def test_html_escapes_the_address():
    mail = build_reset_email(to='a"<b>@example.com', token="t")
    assert "<b>" not in mail.html_body
