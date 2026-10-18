"""Which Nextcloud server this deployment connects to.

One server for everyone, set by the admin as ``NEXTCLOUD_BASE_URL``; users
never type an address, so no request can be pointed anywhere else.
"""

from __future__ import annotations

from urllib.parse import urlsplit

from app.config import config


class NextcloudNotConfiguredError(Exception):
    """NEXTCLOUD_BASE_URL is unset or not a usable URL."""


def normalize_server_url(raw: str) -> str | None:
    """``https://host[/subpath]`` without a trailing slash; None unless plain http(s) with a host."""
    url = (raw or "").strip().rstrip("/")
    parts = urlsplit(url)
    if parts.scheme not in ("http", "https") or not parts.netloc:
        return None
    if parts.query or parts.fragment or "@" in parts.netloc:
        return None
    # A WebDAV URL copied from Nextcloud's settings names the same server.
    path = parts.path
    marker = path.find("/remote.php")
    if marker >= 0:
        path = path[:marker]
    return f"{parts.scheme}://{parts.netloc}{path.rstrip('/')}"


def configured_server_url() -> str | None:
    return normalize_server_url(config.NEXTCLOUD_BASE_URL or "")


def require_server_url() -> str:
    url = configured_server_url()
    if url is None:
        raise NextcloudNotConfiguredError("Nextcloud is not set up on this server")
    return url


def server_label(url: str) -> str:
    """What people see: the host (and sub-path), e.g. ``cloud.example.com``."""
    parts = urlsplit(url)
    return f"{parts.netloc}{parts.path}"
