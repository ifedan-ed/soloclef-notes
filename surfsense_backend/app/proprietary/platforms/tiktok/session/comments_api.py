"""A video's comments over TikTok's plain web API, with the browser as fallback.

``/api/comment/list/`` answers an unsigned GET carrying ``aid=1988``, the video
id, ``count`` and ``cursor`` (checked 2026-10-09 from the home proxy and from
the server's own address). The browser route (open the video page, click the
comment icon, capture the XHR) now lands on TikTok's "Oops! Something went
wrong" page and never fires, so the API goes first and the browser is tried only
when the API did not answer at all. A valid empty answer (a video with no
comments) is final: no browser run.
"""

from __future__ import annotations

import logging
import re
from collections.abc import Awaitable, Callable
from typing import Any
from urllib.parse import urlencode

from ..extraction import comments_from_response
from .client import fetch_json
from .listing import fetch_comments as fetch_comments_in_browser

logger = logging.getLogger(__name__)

_API_URL = "https://www.tiktok.com/api/comment/list/"
# TikTok's web app id; the endpoint ignores requests without it.
_AID = "1988"
# The most one page returns (asked for 50, got 47-50: removed comments still
# count against the page).
PAGE_SIZE = 50
# Paging bound so a huge thread or a cursor that stops moving can't loop:
# 20 pages is up to 1,000 comments, above the tiktok.comments cap (100 a call).
MAX_PAGES = 20
_VIDEO_ID_RE = re.compile(r"/(?:video|photo)/(\d+)")

FetchJson = Callable[[str], Awaitable[Any | None]]
FetchInBrowser = Callable[[str, int], Awaitable[list[dict[str, Any]]]]


def video_id(url: str) -> str | None:
    """The numeric id in a ``/@user/video/<id>`` (or ``/photo/<id>``) URL."""
    match = _VIDEO_ID_RE.search(url)
    return match.group(1) if match else None


def page_url(aweme_id: str, count: int, cursor: int) -> str:
    query = {"aid": _AID, "aweme_id": aweme_id, "count": count, "cursor": cursor}
    return f"{_API_URL}?{urlencode(query)}"


async def fetch_comment_pages(
    video_url: str, target_count: int, *, fetch: FetchJson = fetch_json
) -> list[dict[str, Any]] | None:
    """Up to ``target_count`` raw comment records, or ``None`` if the API never answered."""
    aweme_id = video_id(video_url)
    if aweme_id is None or target_count <= 0:
        return None
    collected: list[dict[str, Any]] = []
    cursor = 0
    for _ in range(MAX_PAGES):
        count = min(PAGE_SIZE, target_count - len(collected))
        body = await fetch(page_url(aweme_id, count, cursor))
        if not isinstance(body, dict) or body.get("status_code", 0) != 0:
            # No usable answer: what came before still counts; nothing at all
            # tells the caller to try the browser.
            return collected or None
        comments = comments_from_response(body)
        collected.extend(comments)
        if len(collected) >= target_count or not comments or not body.get("has_more"):
            break
        next_cursor = body.get("cursor")
        if not isinstance(next_cursor, int) or next_cursor <= cursor:
            break
        cursor = next_cursor
    return collected[:target_count]


async def fetch_comments(
    video_url: str,
    target_count: int,
    *,
    fetch: FetchJson = fetch_json,
    in_browser: FetchInBrowser = fetch_comments_in_browser,
) -> list[dict[str, Any]]:
    """The ``FetchCommentsFn`` the orchestrator uses: the API, else the browser."""
    comments = await fetch_comment_pages(video_url, target_count, fetch=fetch)
    if comments is not None:
        return comments
    logger.info(
        "[tiktok] comment API gave nothing for %s; trying the browser", video_url
    )
    return await in_browser(video_url, target_count)
