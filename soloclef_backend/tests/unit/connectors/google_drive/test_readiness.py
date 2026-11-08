"""What the server still needs before Drive can connect, and the Picker's app id."""

import pytest

from app.config import config
from app.connectors.google_drive.connect.readiness import (
    missing_settings,
    picker_app_id,
)

pytestmark = pytest.mark.unit


def test_a_fully_configured_server_misses_nothing():
    assert missing_settings() == []


def test_the_picker_app_id_is_the_client_ids_project_number():
    assert picker_app_id() == "123456789012"


def test_an_explicit_app_id_wins(monkeypatch):
    monkeypatch.setattr(config, "GOOGLE_PICKER_APP_ID", "999")
    assert picker_app_id() == "999"


def test_unset_settings_are_named_never_valued(monkeypatch):
    monkeypatch.setattr(config, "GOOGLE_DRIVE_REDIRECT_URI", None)
    monkeypatch.setattr(config, "GOOGLE_PICKER_API_KEY", "")
    assert missing_settings() == ["GOOGLE_DRIVE_REDIRECT_URI", "GOOGLE_PICKER_API_KEY"]


def test_a_client_id_of_another_provider_is_not_googles(monkeypatch):
    # e.g. an OIDC client left in GOOGLE_OAUTH_CLIENT_ID for a generic login.
    monkeypatch.setattr(config, "GOOGLE_OAUTH_CLIENT_ID", "soloclef-notes-oidc")
    assert missing_settings() == ["GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_PICKER_APP_ID"]


def test_a_google_client_id_without_a_project_number_needs_the_app_id(monkeypatch):
    monkeypatch.setattr(
        config, "GOOGLE_OAUTH_CLIENT_ID", "custom.apps.googleusercontent.com"
    )
    assert missing_settings() == ["GOOGLE_PICKER_APP_ID"]
