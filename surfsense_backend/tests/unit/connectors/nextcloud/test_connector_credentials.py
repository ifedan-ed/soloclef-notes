"""Stored Nextcloud settings: the configured server only, and the app password at rest."""

import pytest

from app.config import config
from app.connectors.nextcloud.connector_credentials import (
    InvalidNextcloudSettingsError,
    NextcloudReconnectNeededError,
    account_from_config,
    stored_config,
)
from app.connectors.nextcloud.server_address import (
    configured_server_url,
    normalize_server_url,
    server_label,
)

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ("https://cloud.example.com/", "https://cloud.example.com"),
        (" https://cloud.example.com ", "https://cloud.example.com"),
        (
            "https://example.com/nextcloud/remote.php/dav/files/alice/",
            "https://example.com/nextcloud",
        ),
    ],
)
def test_the_address_is_reduced_to_the_server(raw, expected):
    assert normalize_server_url(raw) == expected


@pytest.mark.parametrize(
    "raw",
    [
        "",
        "cloud.example.com",
        "ftp://cloud.example.com",
        "https://u:p@cloud.example.com",
    ],
)
def test_addresses_that_are_not_plain_http_urls_are_not_servers(raw):
    assert normalize_server_url(raw) is None


def test_the_server_comes_from_the_admins_setting(monkeypatch):
    monkeypatch.setattr(config, "NEXTCLOUD_BASE_URL", "https://cloud.example.com/")
    assert configured_server_url() == "https://cloud.example.com"
    assert server_label(configured_server_url()) == "cloud.example.com"
    monkeypatch.setattr(config, "NEXTCLOUD_BASE_URL", None)
    assert configured_server_url() is None


def test_the_app_password_is_stored_encrypted_and_read_back():
    config_ = stored_config(
        "https://cloud.example.com",
        "alice",
        "s3cret-app-pw",
        login_name="alice@example.com",
        display_name="Alice",
    )
    assert config_["_token_encrypted"] is True
    assert config_["auth_method"] == "login_flow"
    assert "s3cret-app-pw" not in str(config_)
    account = account_from_config(config_)
    assert account.app_password == "s3cret-app-pw"
    assert account.username == "alice"
    assert account.auth_name == "alice@example.com"


def test_a_row_saved_from_a_pasted_password_keeps_working():
    legacy = stored_config("https://cloud.example.com", "alice", "pw")
    for key in ("auth_method", "login_name", "display_name"):
        legacy.pop(key)
    account = account_from_config(legacy)
    assert account.auth_name == "alice"


def test_a_login_for_another_server_needs_a_reconnect(monkeypatch):
    other = stored_config("https://evil.example.net", "alice", "pw")
    with pytest.raises(NextcloudReconnectNeededError):
        account_from_config(other)
    monkeypatch.setattr(config, "NEXTCLOUD_BASE_URL", None)
    with pytest.raises(NextcloudReconnectNeededError):
        account_from_config(stored_config("https://cloud.example.com", "alice", "pw"))


def test_a_missing_password_or_username_is_refused():
    with pytest.raises(InvalidNextcloudSettingsError):
        stored_config("https://cloud.example.com", "alice", " ")
    with pytest.raises(InvalidNextcloudSettingsError):
        stored_config("https://cloud.example.com", "", "pw")
    with pytest.raises(InvalidNextcloudSettingsError):
        account_from_config({"server_url": "https://cloud.example.com"})
