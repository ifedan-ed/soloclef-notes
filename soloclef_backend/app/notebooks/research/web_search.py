"""General web search: the bundled SearXNG first, Google Search when it is unset or empty.

SearXNG is what the Google Search scraper itself falls back to, and is free and
fast, so "web" asks it directly. When no instance is configured (or it answers
nothing) the Google Search capability runs instead, billed like any scrape.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable

from app.proprietary.platforms.google_search import (
    GoogleSearchScrapeInput,
    SerpItem,
    searxng,
)

from .capability_runner import CapabilityRunner
from .schemas import ResultRow
from .sources import serp

SearxngPage = Callable[[str, int], Awaitable[SerpItem | None]]


async def searxng_page(query: str, page: int) -> SerpItem | None:
    """One SearXNG results page, or None when no instance is configured."""
    if not searxng.enabled():
        return None
    return await searxng.search_serp(
        query, GoogleSearchScrapeInput(queries=query), page=page
    )


async def search_web(
    query: str,
    limit: int,
    runner: CapabilityRunner,
    searxng_fetch: SearxngPage = searxng_page,
) -> list[ResultRow]:
    items: list[SerpItem] = []
    for page in range(1, serp.pages_for(limit) + 1):
        item = await searxng_fetch(query, page)
        if item is None:
            break
        items.append(item)
    rows = serp.serp_rows(items, "web")
    if rows:
        return rows
    output = await runner.run(serp.PLAN.capability, serp.build_input(query, limit))
    return serp.PLAN.to_rows(output, "web")
