"""Read an ordinary web page by crawling it."""

from __future__ import annotations

from ..capability_runner import CapabilityRunner
from ..page_reader import read_page
from .fetched import FetchedContent


async def fetch_web_page(runner: CapabilityRunner, url: str) -> FetchedContent | None:
    markdown, metadata = await read_page(runner, url)
    if not markdown:
        return None
    metadata = metadata or {}
    return FetchedContent(
        markdown=markdown,
        title=metadata.get("title"),
        metadata={"page_title": metadata["title"]} if metadata.get("title") else {},
    )
