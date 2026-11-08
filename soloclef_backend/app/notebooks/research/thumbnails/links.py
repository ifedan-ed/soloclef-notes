"""Turn a result's image URLs into proxied links, and give web results a favicon."""

from __future__ import annotations

from urllib.parse import quote, urlsplit

from ..schemas import ResultRow
from ..urls import is_http_url
from .signing import encode_url, signature

PROXY_PATH = "/api/v1/research/thumbnails"
FAVICON_SERVICE = "https://www.google.com/s2/favicons?sz=64&domain={host}"
_WEB_SOURCES = frozenset({"web", "google_search"})


def proxied(url: str | None) -> str | None:
    if not is_http_url(url):
        return None
    url = url.strip()
    return f"{PROXY_PATH}?u={encode_url(url)}&s={signature(url)}"


def favicon_for(page_url: str) -> str | None:
    host = (urlsplit(page_url).hostname or "").lower()
    if not host:
        return None
    return proxied(FAVICON_SERVICE.format(host=quote(host)))


def with_proxied_images(row: ResultRow) -> ResultRow:
    update: dict[str, str | None] = {"thumbnail_url": proxied(row.thumbnail_url)}
    if row.source in _WEB_SOURCES:
        update["favicon_url"] = favicon_for(row.url)
    return row.model_copy(update=update)
