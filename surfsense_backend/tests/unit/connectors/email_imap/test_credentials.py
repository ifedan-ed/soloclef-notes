"""Encrypted account record round-trip and the generic-write guard."""

from __future__ import annotations

import pytest

from app.connectors.email_imap import credentials as creds

pytestmark = pytest.mark.unit


@pytest.fixture(autouse=True)
def _host_is_publicly_routable(monkeypatch):
    """Unit tests must not depend on real DNS/network. Every test below
    that is not specifically exercising the SSRF guard gets a host that
    passes it; ``test_new_account_record_rejects_a_non_public_host`` below
    overrides this per-case to exercise the refusal."""
    monkeypatch.setattr(creds, "is_publicly_routable", lambda _url: True)


def test_new_account_record_encrypts_the_password(monkeypatch):
    from app.config import config

    monkeypatch.setattr(config, "SECRET_KEY", "unit-test-secret-key")

    record = creds.new_account_record(
        label="Work",
        host="mail.example.com",
        port=993,
        use_tls=True,
        username="me@example.com",
        app_password="hunter2",
    )

    assert record["_encrypted"] is True
    assert record["app_password"] != "hunter2"
    assert "hunter2" not in str(record)

    assert creds.decrypt_password(record) == "hunter2"


def test_public_account_never_includes_the_password(monkeypatch):
    from app.config import config

    monkeypatch.setattr(config, "SECRET_KEY", "unit-test-secret-key")
    record = creds.new_account_record(
        label="Work",
        host="mail.example.com",
        port=993,
        use_tls=True,
        username="me@example.com",
        app_password="hunter2",
    )
    public = creds.public_account(record)
    assert "app_password" not in public
    assert "hunter2" not in str(public)
    assert public["username"] == "me@example.com"


def test_missing_fields_are_rejected():
    with pytest.raises(creds.InvalidEmailAccountError):
        creds.new_account_record(
            label="x", host="", port=993, use_tls=True, username="u", app_password="p"
        )
    with pytest.raises(creds.InvalidEmailAccountError):
        creds.new_account_record(
            label="x",
            host="h",
            port=993,
            use_tls=True,
            username="",
            app_password="p",
        )
    with pytest.raises(creds.InvalidEmailAccountError):
        creds.new_account_record(
            label="x",
            host="h",
            port=993,
            use_tls=True,
            username="u",
            app_password="",
        )
    with pytest.raises(creds.InvalidEmailAccountError):
        creds.new_account_record(
            label="x",
            host="h",
            port=0,
            use_tls=True,
            username="u",
            app_password="p",
        )


def test_decrypt_rejects_unencrypted_or_empty_records():
    with pytest.raises(creds.InvalidEmailAccountError):
        creds.decrypt_password({"app_password": "plaintext"})
    with pytest.raises(creds.InvalidEmailAccountError):
        creds.decrypt_password({"_encrypted": True, "app_password": ""})


def test_assert_accounts_are_encrypted_blocks_plaintext_writes():
    # None/absent is fine (no-op update).
    creds.assert_accounts_are_encrypted(None)

    # A properly encrypted account passes.
    creds.assert_accounts_are_encrypted(
        [{"_encrypted": True, "app_password": "cipher"}]
    )

    # A plaintext or tampered account is rejected — this is what stops the
    # *generic* connector PUT from ever writing a raw password.
    with pytest.raises(ValueError):
        creds.assert_accounts_are_encrypted(
            [{"host": "evil.example.com", "app_password": "plaintext"}]
        )
    with pytest.raises(ValueError):
        creds.assert_accounts_are_encrypted("not-a-list")


def test_new_account_record_rejects_a_non_public_host(monkeypatch):
    """SSRF guard: a host that does not resolve to a public address (an
    internal service name, a private IP, the cloud metadata address, ...)
    is refused before any credential is even encrypted."""
    from app.config import config

    monkeypatch.setattr(config, "SECRET_KEY", "unit-test-secret-key")
    monkeypatch.setattr(creds, "is_publicly_routable", lambda _url: False)

    with pytest.raises(creds.InvalidEmailAccountError):
        creds.new_account_record(
            label="x",
            host="redis",  # a Docker service name on the shared networks
            port=993,
            use_tls=True,
            username="u",
            app_password="p",
        )


def test_new_account_record_checks_the_host_as_a_url(monkeypatch):
    """The guard is reused as-is (``is_publicly_routable``), given the host
    wrapped as a URL it can parse — including a bare IPv6 literal, which
    needs bracketing or ``urlsplit`` misreads part of the address as a port."""
    from app.config import config

    monkeypatch.setattr(config, "SECRET_KEY", "unit-test-secret-key")
    seen: list[str] = []

    def _record(url: str) -> bool:
        seen.append(url)
        return True

    monkeypatch.setattr(creds, "is_publicly_routable", _record)

    creds.new_account_record(
        label="x",
        host="2001:db8::1",
        port=993,
        use_tls=True,
        username="u",
        app_password="p",
    )
    assert seen == ["https://[2001:db8::1]/"]


def test_accounts_from_config_tolerates_missing_or_malformed_data():
    assert creds.accounts_from_config(None) == []
    assert creds.accounts_from_config({}) == []
    assert creds.accounts_from_config({"accounts": "nope"}) == []
    assert creds.accounts_from_config({"accounts": [{"id": "a"}, "junk"]}) == [
        {"id": "a"}
    ]


_STORED_PASSWORD = {
    "id": "a1",
    "label": "Work",
    "host": "mail.example.com",
    "port": 993,
    "use_tls": True,
    "username": "me@example.com",
    "app_password": "ciphertext-of-the-password",
    "_encrypted": True,
}
_STORED_MICROSOFT = {
    "id": "m1",
    "label": "me@contoso.example",
    "host": "outlook.office365.com",
    "port": 993,
    "use_tls": True,
    "username": "me@contoso.example",
    "auth": "microsoft",
    "microsoft_subject": "tid:oid",
    "access_token": "ciphertext-of-the-access-token",
    "refresh_token": "ciphertext-of-the-refresh-token",
    "access_token_expires_at": "2026-01-01T00:00:00+00:00",
    "_encrypted": True,
}


def test_public_config_keeps_no_ciphertext_and_no_marker():
    cfg = {"accounts": [_STORED_PASSWORD, _STORED_MICROSOFT], "other": 1}
    shown = creds.public_config(cfg)
    assert shown["other"] == 1
    assert [a["id"] for a in shown["accounts"]] == ["a1", "m1"]
    assert [a["auth"] for a in shown["accounts"]] == ["password", "microsoft"]
    assert "ciphertext" not in str(shown)
    assert "_encrypted" not in str(shown)
    # The stored config itself is left alone.
    assert cfg["accounts"][1]["refresh_token"] == "ciphertext-of-the-refresh-token"


def test_public_config_without_accounts_is_unchanged():
    assert creds.public_config({"x": 1}) == {"x": 1}
    assert creds.public_config(None) == {}


def test_keep_stored_accounts_ignores_whatever_accounts_were_sent():
    stored = {"accounts": [_STORED_PASSWORD]}
    sent = {"accounts": [_STORED_MICROSOFT], "other": 2}
    kept = creds.keep_stored_accounts(sent, stored)
    assert kept == {"accounts": [_STORED_PASSWORD], "other": 2}


def test_keep_stored_accounts_adds_none_when_none_are_stored():
    kept = creds.keep_stored_accounts({"accounts": [_STORED_MICROSOFT]}, {})
    assert kept == {}
