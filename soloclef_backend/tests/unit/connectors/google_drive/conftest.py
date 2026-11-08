"""Every Drive unit test runs with a web client configured, as a deployment is."""

import pytest

from app.config import config


@pytest.fixture(autouse=True)
def _configured_google(monkeypatch):
    monkeypatch.setattr(
        config, "GOOGLE_OAUTH_CLIENT_ID", "123456789012-abc.apps.googleusercontent.com"
    )
    monkeypatch.setattr(config, "GOOGLE_OAUTH_CLIENT_SECRET", "client-secret")
    monkeypatch.setattr(
        config,
        "GOOGLE_DRIVE_REDIRECT_URI",
        "https://notes.example.com/api/v1/auth/google/drive/connector/callback",
    )
    monkeypatch.setattr(config, "GOOGLE_PICKER_API_KEY", "picker-key")
    monkeypatch.setattr(config, "GOOGLE_PICKER_APP_ID", None)
    monkeypatch.setattr(config, "SECRET_KEY", config.SECRET_KEY or "unit-test-secret")
