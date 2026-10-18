"""Fetch one remote image for the proxy: public hosts only, small, and really an image."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass

import httpx

from app.utils.crawl import is_publicly_routable

MAX_IMAGE_BYTES = 3 * 1024 * 1024
_MAX_REDIRECTS = 4
_TIMEOUT_S = 10.0
# SVG is left out on purpose: opened directly it is a document that can script.
ALLOWED_TYPES = frozenset(
    {
        "image/jpeg",
        "image/png",
        "image/gif",
        "image/webp",
        "image/avif",
        "image/x-icon",
        "image/vnd.microsoft.icon",
    }
)
_HEADERS = {
    "User-Agent": "Mozilla/5.0 (compatible; SurfSense thumbnail fetcher)",
    "Accept": "image/avif,image/webp,image/png,image/jpeg,image/*;q=0.8",
    "Accept-Encoding": "gzip",
}


class ImageRefusedError(Exception):
    """Not fetched: private host, too big, not an image, or the host said no."""


@dataclass(frozen=True)
class FetchedImage:
    body: bytes
    content_type: str


async def fetch_image(
    url: str, *, transport: httpx.AsyncBaseTransport | None = None
) -> FetchedImage:
    async with httpx.AsyncClient(
        timeout=_TIMEOUT_S,
        follow_redirects=False,
        headers=_HEADERS,
        transport=transport,
    ) as client:
        for _ in range(_MAX_REDIRECTS + 1):
            if not await asyncio.to_thread(is_publicly_routable, url):
                raise ImageRefusedError("non-public address")
            async with client.stream("GET", url) as response:
                if response.is_redirect:
                    url = str(response.url.join(response.headers["location"]))
                    continue
                if response.status_code != 200:
                    raise ImageRefusedError(f"status {response.status_code}")
                kind = response.headers.get("content-type", "").split(";")[0].strip()
                if kind.lower() not in ALLOWED_TYPES:
                    raise ImageRefusedError(f"type {kind!r}")
                body = bytearray()
                async for chunk in response.aiter_bytes():
                    body.extend(chunk)
                    if len(body) > MAX_IMAGE_BYTES:
                        raise ImageRefusedError("too large")
                return FetchedImage(bytes(body), kind.lower())
    raise ImageRefusedError("too many redirects")
