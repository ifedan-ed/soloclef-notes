"""Every Nextcloud test runs against one configured server, as a deployment does."""

import pytest

from app.config import config


@pytest.fixture(autouse=True)
def _configured_nextcloud(monkeypatch):
    monkeypatch.setattr(config, "NEXTCLOUD_BASE_URL", "https://cloud.example.com")
    monkeypatch.setattr(config, "SECRET_KEY", config.SECRET_KEY or "unit-test-secret")
