"""Fetch a page on another Google property (Scholar) through the SERP fetch seam.

The SERP path vets IPs with a cheap GET and only renders on IPs that pass,
which suits a rotating pool. Scholar is served as plain HTML, but it walls an
IP with the same ``/sorry`` reCAPTCHA as Search — and on a single static proxy
(a home connection) there is no other IP to move to. So this tries the cheap
GET first, carrying any exemption cookie already earned on that proxy, and
only when that is walled renders in the shared browser, where the SERP seam's
page action solves ``/sorry`` and caches the exemption for the next call.
"""

from __future__ import annotations

import logging
from collections.abc import Callable

from scrapling.fetchers import AsyncFetcher

from app.utils.proxy import get_proxy_url

from . import fetch as _serp

logger = logging.getLogger(__name__)

_GET_TIMEOUT_S = 20.0
# The solver sometimes returns "unsolvable" for a /sorry page it clears on the
# next try (seen live on Scholar), so a walled render gets one more go.
_RENDER_ATTEMPTS = 2


def _cookie_header(proxy: str | None) -> dict[str, str]:
    cookies = dict(_serp.CONSENT_COOKIES)
    for cookie in _serp._exemption_jar.get(proxy or "", []):
        if cookie.get("name") and cookie.get("value"):
            cookies[cookie["name"]] = cookie["value"]
    return cookies


async def _plain_get(url: str, proxy: str | None) -> str | None:
    try:
        page = await AsyncFetcher.get(
            url,
            cookies=_cookie_header(proxy),
            proxy=proxy,
            stealthy_headers=True,
            timeout=_GET_TIMEOUT_S,
        )
    except Exception as exc:
        logger.info("[google_property] plain GET failed: %r", exc)
        return None
    return page.html_content if page.status == 200 else None


async def fetch_google_property_html(
    url: str, *, has_results: Callable[[str], bool]
) -> str | None:
    """The page's HTML once ``has_results`` accepts it, or None when walled."""
    proxy = get_proxy_url("google_search")
    html = await _plain_get(url, proxy)
    if html and has_results(html):
        return html
    if _serp.AsyncStealthySession is None:
        return None
    for attempt in range(1, _RENDER_ATTEMPTS + 1):
        try:
            page = await _serp._render(url, proxy)
        except Exception as exc:
            logger.warning("[google_property] render failed: %r", exc)
            await _serp._drop_session(False)
            continue
        rendered = page.html_content or ""
        if page.status == 200 and has_results(rendered):
            return rendered
        logger.warning(
            "[google_property] no results for %s (attempt %d, status=%s, walled=%s)",
            url.split("?")[0],
            attempt,
            page.status,
            _serp._is_walled(rendered),
        )
    return None
