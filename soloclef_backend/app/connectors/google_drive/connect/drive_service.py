"""A Drive v3 API client for one grant, with a transport that ignores proxy settings.

httplib2 (under googleapiclient) raises on any configured HTTP(S)_PROXY when
PySocks is absent, even for hosts listed in NO_PROXY, so the transport is
built without proxy info: Drive calls go direct, as they always did in
practice.
"""

from __future__ import annotations

import httplib2
from google.oauth2.credentials import Credentials
from google_auth_httplib2 import AuthorizedHttp
from googleapiclient.discovery import build

from .google_endpoints import drive_api_endpoint

_TIMEOUT_SECONDS = 120


def authorized_http(credentials: Credentials) -> AuthorizedHttp:
    """A fresh transport (``httplib2.Http`` is not thread-safe)."""
    return AuthorizedHttp(
        credentials, http=httplib2.Http(timeout=_TIMEOUT_SECONDS, proxy_info=None)
    )


def drive_service(credentials: Credentials):
    endpoint = drive_api_endpoint()
    return build(
        "drive",
        "v3",
        http=authorized_http(credentials),
        client_options={"api_endpoint": endpoint} if endpoint else None,
        cache_discovery=False,
    )
