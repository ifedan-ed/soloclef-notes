"""Search Google Scholar slowly, remember the answers, and say so when it walls us.

There is no Scholar API, and scraping it is against Google's terms and quickly
blocked; the owner asked for it knowing that. So each process sends at most one
Scholar request per ``SCHOLAR_MIN_INTERVAL_S`` (later ones queue), a repeated
search is served from memory for ``SCHOLAR_CACHE_TTL_S``, and a walled fetch
becomes a clear message instead of an empty list.
"""

from __future__ import annotations

import threading
import time
from collections.abc import Awaitable, Callable
from urllib.parse import quote_plus

from app.config import config

from ...source_errors import SourceUnavailableError
from ..polite_http import HostPacer
from .parse import ScholarHit, page_has_results, parse_results

# Takes a Scholar URL; returns the results page HTML, or None when walled.
FetchScholarPage = Callable[[str], Awaitable[str | None]]

PER_PAGE = 20
BLOCKED = (
    "Google Scholar is blocking automated searches right now. Try again in a few "
    "minutes, or search PubMed, arXiv or medRxiv instead."
)
_CACHE_LIMIT = 200

_pacer = HostPacer(lambda host: config.SCHOLAR_MIN_INTERVAL_S)
_cache: dict[tuple[str, int], tuple[float, list[ScholarHit]]] = {}
_cache_lock = threading.Lock()


def page_url(query: str, start: int, count: int) -> str:
    return (
        "https://scholar.google.com/scholar?hl=en"
        f"&q={quote_plus(query)}&num={count}&start={start}"
    )


def _cached(key: tuple[str, int]) -> list[ScholarHit] | None:
    with _cache_lock:
        entry = _cache.get(key)
        if entry and entry[0] > time.monotonic():
            return entry[1]
        _cache.pop(key, None)
    return None


def _remember(key: tuple[str, int], hits: list[ScholarHit]) -> None:
    with _cache_lock:
        if len(_cache) >= _CACHE_LIMIT:
            _cache.pop(next(iter(_cache)))
        _cache[key] = (time.monotonic() + config.SCHOLAR_CACHE_TTL_S, hits)


def clear_cache() -> None:
    with _cache_lock:
        _cache.clear()


async def search_scholar(
    query: str, limit: int, fetch_page: FetchScholarPage
) -> list[ScholarHit]:
    key = (" ".join(query.lower().split()), limit)
    if (hits := _cached(key)) is not None:
        return hits
    hits: list[ScholarHit] = []
    for start in range(0, limit, PER_PAGE):
        await _pacer.wait("scholar.google.com")
        html = await fetch_page(page_url(query, start, min(PER_PAGE, limit - start)))
        if html is None or not page_has_results(html):
            if hits:
                break  # keep the first page rather than lose it to the second
            raise SourceUnavailableError(BLOCKED, code="source_blocked")
        page = parse_results(html)
        hits.extend(page)
        if len(page) < min(PER_PAGE, limit - start):
            break
    hits = hits[:limit]
    _remember(key, hits)
    return hits


async def fetch_scholar_page(url: str) -> str | None:
    """Production fetch: the Google fetch seam (proxy, browser, captcha solve)."""
    from app.proprietary.platforms.google_search.property_fetch import (
        fetch_google_property_html,
    )

    return await fetch_google_property_html(url, has_results=page_has_results)
