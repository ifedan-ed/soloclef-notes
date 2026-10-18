"""``GET /research/thumbnails``: serve a signed result image from our own origin.

No session is required, because an ``<img>`` cannot carry the bearer token; the
signature is the permission, and it only covers URLs a search put in a result.
"""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable

from fastapi import APIRouter, Depends, Query, Response

from .fetch_image import FetchedImage, ImageRefusedError, fetch_image
from .signing import decode_url, is_signed

logger = logging.getLogger(__name__)

router = APIRouter()

ImageFetcher = Callable[[str], Awaitable[FetchedImage]]

_CACHE = "public, max-age=86400, immutable"
_SAFE_HEADERS = {
    "X-Content-Type-Options": "nosniff",
    "Content-Security-Policy": "default-src 'none'; sandbox",
    "Cross-Origin-Resource-Policy": "cross-origin",
    "Referrer-Policy": "no-referrer",
}


def get_image_fetcher() -> ImageFetcher:
    return fetch_image


def _missing() -> Response:
    # Short cache: a CDN hiccup should not blank the tile for a day.
    return Response(status_code=404, headers={"Cache-Control": "public, max-age=300"})


@router.get("/research/thumbnails")
async def thumbnail(
    u: str = Query(max_length=4096),
    s: str = Query(max_length=64),
    fetch: ImageFetcher = Depends(get_image_fetcher),
) -> Response:
    url = decode_url(u)
    if not url or not is_signed(url, s):
        return Response(status_code=404)
    try:
        image = await fetch(url)
    except ImageRefusedError as refused:
        logger.debug("thumbnail refused (%s): %s", refused, url)
        return _missing()
    except Exception:
        logger.info("thumbnail fetch failed: %s", url, exc_info=True)
        return _missing()
    return Response(
        content=image.body,
        media_type=image.content_type,
        headers={"Cache-Control": _CACHE, **_SAFE_HEADERS},
    )
