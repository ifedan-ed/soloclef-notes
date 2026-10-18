"""Modular residential / rotating proxy provider package.

Selects a provider via the ``PROXY_PROVIDER`` env var (see ``registry.py``) and
exposes proxy settings in the formats different HTTP libraries expect. Add new
vendors by implementing :class:`ProxyProvider` in ``providers/`` and registering
them in ``registry.py``.

The URL getters take an optional ``platform`` (a scraper tool id such as
``"reddit"``). A platform listed in ``SCRAPER_DIRECT_PLATFORMS`` gets ``None``,
so that scraper fetches from the server's own address (see ``direct.py``).
"""

from app.utils.proxy.base import ProxyProvider
from app.utils.proxy.direct import goes_direct
from app.utils.proxy.registry import get_active_provider


def get_proxy_url(platform: str | None = None) -> str | None:
    """Canonical ``http://user:pass@host:port`` URL for Scrapling/curl_cffi."""
    if goes_direct(platform):
        return None
    return get_active_provider().get_proxy_url()


def get_geo_proxy_url(
    country: str | None = None, *, platform: str | None = None
) -> str | None:
    """Proxy URL pinned to an exit country when the provider supports it."""
    if goes_direct(platform):
        return None
    return get_active_provider().get_geo_proxy_url(country)


def get_sticky_proxy_url(
    session_id: str, country: str | None = None, *, platform: str | None = None
) -> str | None:
    """Proxy URL pinned to a stable vendor session when supported."""
    if goes_direct(platform):
        return None
    return get_active_provider().get_sticky_proxy_url(session_id, country)


def get_playwright_proxy() -> dict[str, str] | None:
    """Playwright-style proxy dict, or ``None`` when not configured."""
    return get_active_provider().get_playwright_proxy()


def get_requests_proxies(platform: str | None = None) -> dict[str, str] | None:
    """``{"http": ..., "https": ...}`` dict for requests/aiohttp, or ``None``."""
    if goes_direct(platform):
        return None
    return get_active_provider().get_requests_proxies()


def is_pool_backed() -> bool:
    """Whether the active provider rotates across a client-side pool of endpoints.

    The crawler gates its bounded proxy-error rotation-retry on this.
    """
    return get_active_provider().is_pool_backed


def get_residential_proxy_url() -> str | None:
    """Backward-compatible alias for :func:`get_proxy_url`."""
    return get_proxy_url()


__all__ = [
    "ProxyProvider",
    "get_active_provider",
    "get_geo_proxy_url",
    "get_playwright_proxy",
    "get_proxy_url",
    "get_requests_proxies",
    "get_residential_proxy_url",
    "get_sticky_proxy_url",
    "goes_direct",
    "is_pool_backed",
]
