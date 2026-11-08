"""Pick the right reader for an imported URL, falling back to a plain crawl."""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from uuid import UUID

from sqlalchemy.ext.asyncio import AsyncSession

from ..capability_runner import CapabilityRunner
from .fetched import FetchedContent
from .kind import is_remote_file, is_youtube_video
from .remote_file import fetch_remote_file
from .web_page import fetch_web_page
from .youtube_video import fetch_youtube_video

logger = logging.getLogger(__name__)

FileFetcher = Callable[
    [AsyncSession, str, UUID | str], Awaitable[FetchedContent | None]
]


async def _read_specialised(
    session: AsyncSession,
    runner: CapabilityRunner,
    url: str,
    user_id: UUID | str,
    file_fetcher: FileFetcher,
) -> FetchedContent | None:
    if is_youtube_video(url):
        return await fetch_youtube_video(runner, url)
    if is_remote_file(url):
        return await file_fetcher(session, url, user_id)
    return None


async def fetch_content(
    session: AsyncSession,
    runner: CapabilityRunner,
    url: str,
    user_id: UUID | str,
    *,
    file_fetcher: FileFetcher = fetch_remote_file,
) -> FetchedContent | None:
    """The URL's content, or None when every reader came back empty or failed."""
    try:
        content = await _read_specialised(session, runner, url, user_id, file_fetcher)
    except Exception:
        logger.info("specialised read of %s failed; crawling it", url, exc_info=True)
        content = None
    if content is not None:
        return content
    try:
        return await fetch_web_page(runner, url)
    except Exception:
        logger.info("crawling %s failed", url, exc_info=True)
        return None
