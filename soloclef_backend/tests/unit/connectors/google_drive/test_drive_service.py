"""Drive calls never go through a configured HTTP proxy (httplib2 cannot use one here)."""

import pytest
from google.oauth2.credentials import Credentials

from app.connectors.google_drive.connect.drive_service import authorized_http

pytestmark = pytest.mark.unit


def test_the_transport_ignores_proxy_settings(monkeypatch):
    monkeypatch.setenv("HTTPS_PROXY", "http://127.0.0.1:1")
    monkeypatch.setenv("HTTP_PROXY", "http://127.0.0.1:1")
    transport = authorized_http(Credentials(token="t"))
    assert transport.http.proxy_info is None
