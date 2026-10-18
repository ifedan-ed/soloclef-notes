"""Deep research: read the top pages and replace their snippets with the passage that answers the query."""

from __future__ import annotations

import logging
from collections.abc import AsyncIterator, Awaitable, Callable

from .schemas import ResultRow
from .snippets import excerpt
from .urls import is_http_url

logger = logging.getLogger(__name__)

PAGES_TO_READ = 5

# Takes a URL, returns that page's markdown (None when it could not be read),
# or ``(markdown, image_url)`` when the reader also found the page's og:image.
PageReader = Callable[[str], Awaitable[str | tuple[str | None, str | None] | None]]


def pages_worth_reading(rows: list[ResultRow], count: int = PAGES_TO_READ) -> list[int]:
    """Indexes of the top rows that are ordinary web pages (platform posts read poorly)."""
    picked = [i for i, row in enumerate(rows) if row.source in ("web", "google_search")]
    return picked[:count]


async def enrich(
    rows: list[ResultRow], query: str, read_page: PageReader
) -> AsyncIterator[tuple[int, int, list[ResultRow]]]:
    """Yield ``(done, total, rows)`` after each page; the rows carry better snippets.

    A page that fails to load, or yields nothing usable, keeps its search snippet.
    A page's og:image, when the reader found one, becomes the row's thumbnail.
    """
    rows = list(rows)
    targets = pages_worth_reading(rows)
    total = len(targets)
    for done, index in enumerate(targets, start=1):
        row = rows[index]
        try:
            read = await read_page(row.url)
        except Exception:
            logger.info("deep research could not read %s", row.url, exc_info=True)
            read = None
        markdown, image = read if isinstance(read, tuple) else (read, None)
        update = {}
        better = excerpt(markdown, query) if markdown else ""
        if better:
            update["snippet"] = better
        if image and not row.thumbnail_url and is_http_url(image):
            update["thumbnail_url"] = image.strip()
        if update:
            rows[index] = row.model_copy(update=update)
        yield done, total, rows
