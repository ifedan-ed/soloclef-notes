"""Read one web page as markdown through the ``web.crawl`` capability."""

from __future__ import annotations

from app.capabilities.web.crawl.definition import WEB_CRAWL
from app.capabilities.web.crawl.schemas import CrawlInput

from .capability_runner import CapabilityRunner

MAX_PAGE_CHARS = 200_000


async def read_page(
    runner: CapabilityRunner, url: str, *, max_chars: int = MAX_PAGE_CHARS
) -> tuple[str | None, dict[str, str] | None]:
    """The page's markdown and metadata, or ``(None, None)`` when the crawl failed."""
    output = await runner.run(
        WEB_CRAWL,
        CrawlInput(
            startUrls=[url], maxCrawlDepth=0, maxCrawlPages=1, maxLength=max_chars
        ),
    )
    for item in output.items:
        if item.status == "success" and item.markdown and item.markdown.strip():
            return item.markdown, item.metadata
    return None, None
