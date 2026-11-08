"""Which platform scrapers skip the proxy and leave from the server's own address.

``SCRAPER_DIRECT_PLATFORMS`` (comma-separated, see ``app.config``) names them.
Every proxy getter in :mod:`app.utils.proxy` takes an optional ``platform``; for
a listed platform it returns ``None``, which each scraper already treats as "no
proxy configured" and fetches directly. Callers that pass no platform (the web
crawler, captcha solving) are never affected.

Platform names are the scraper tool ids: ``google_search``, ``youtube``,
``reddit``, ``instagram``, ``tiktok``, ``google_maps``, ``indeed``, ``amazon``,
``walmart``.
"""

from __future__ import annotations

from app.config import Config


def goes_direct(platform: str | None) -> bool:
    """True when ``platform`` is set to skip the proxy (read per call, never cached)."""
    if not platform:
        return False
    return platform.lower() in Config.SCRAPER_DIRECT_PLATFORMS
