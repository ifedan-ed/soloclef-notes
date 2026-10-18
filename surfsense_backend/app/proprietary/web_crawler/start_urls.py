# SurfSense proprietary crawler engine.
#
# Part of the ``app.proprietary`` package; licensed separately from the
# Apache-2.0 project root (see ``app/proprietary/LICENSE``).
"""Fetch start URLs side by side when no links are followed.

With ``maxCrawlDepth=0`` each start URL is an independent fetch. The spider
ran them one after another (its concurrency is pinned to 1 so its page cap
stays exact), and thread 117's crawler spent 170 s on 29 pages in a row. Here
they run side by side, bounded so no site is hammered:

- at most ``START_URL_CONCURRENCY`` fetches at once (``WEB_CRAWL_CONCURRENCY``,
  default 4), and
- at most ``START_URL_PER_HOST_CONCURRENCY`` at once on one site
  (``WEB_CRAWL_PER_HOST_CONCURRENCY``, default 2).

Extra URLs wait their turn; none is dropped. Results keep the order the URLs
were given, and a URL repeated (ignoring its ``#fragment``) is fetched once.
Documented in this package's README ("Multi-page crawls").
"""

from __future__ import annotations

import asyncio
import os
from collections.abc import Callable
from typing import Protocol
from urllib.parse import urldefrag

from app.proprietary.web_crawler.connector import CrawlOutcome, CrawlOutcomeStatus
from app.proprietary.web_crawler.url_policy import host_of


def _read_positive_int(name: str, default: int) -> int:
    try:
        value = int(os.environ.get(name, str(default)))
    except ValueError:
        return default
    return value if value > 0 else default


START_URL_CONCURRENCY = _read_positive_int("WEB_CRAWL_CONCURRENCY", 4)
START_URL_PER_HOST_CONCURRENCY = _read_positive_int("WEB_CRAWL_PER_HOST_CONCURRENCY", 2)


class _Engine(Protocol):
    async def crawl_url(self, url: str) -> CrawlOutcome: ...


# Called as each fetch finishes: (finished so far, total, url, outcome).
OnFetched = Callable[[int, int, str, CrawlOutcome], None]


def unique_start_urls(start_urls: list[str]) -> list[str]:
    """The start URLs in order, each page once (``#fragment`` ignored)."""
    seen: set[str] = set()
    unique: list[str] = []
    for url in start_urls:
        key = urldefrag(url).url.rstrip("?")
        if key in seen:
            continue
        seen.add(key)
        unique.append(url)
    return unique


async def fetch_start_urls(
    engine: _Engine,
    start_urls: list[str],
    *,
    on_fetched: OnFetched | None = None,
    concurrency: int = START_URL_CONCURRENCY,
    per_host_concurrency: int = START_URL_PER_HOST_CONCURRENCY,
) -> list[tuple[str, CrawlOutcome]]:
    """Fetch each start URL once, side by side; results in the given order."""
    urls = unique_start_urls(start_urls)
    overall = asyncio.Semaphore(concurrency)
    per_host: dict[str, asyncio.Semaphore] = {}
    finished = 0

    async def fetch(url: str) -> tuple[str, CrawlOutcome]:
        nonlocal finished
        host_slots = per_host.setdefault(
            host_of(url), asyncio.Semaphore(per_host_concurrency)
        )
        # Host first, so a URL waiting on a busy site does not hold an
        # overall slot another site could use.
        async with host_slots, overall:
            try:
                outcome = await engine.crawl_url(url)
            except Exception as exc:  # crawl_url reports failures itself
                outcome = CrawlOutcome(
                    status=CrawlOutcomeStatus.FAILED,
                    error=f"Error crawling URL {url}: {exc!s}",
                )
        finished += 1
        if on_fetched is not None:
            on_fetched(finished, len(urls), url, outcome)
        return url, outcome

    return list(await asyncio.gather(*(fetch(url) for url in urls)))
