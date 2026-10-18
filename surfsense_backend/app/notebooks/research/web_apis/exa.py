"""Exa search: https://docs.exa.ai/reference/search.

Fast uses Exa's low-latency ``fast`` search; deep uses its default ``auto``
search (it picks the best method per query) and asks for more results. The
agentic ``deep`` types are left alone: slower and several times the price.
Each result carries one short highlight to show as the snippet; the page itself is read by SurfSense's own
crawler when imported, never taken from Exa.
"""

from __future__ import annotations

from typing import Any
from urllib.parse import urlsplit

from ..schemas import ResultRow
from ..snippets import join_meta
from ..sources.rows import keep, make_row
from .requests import ApiRequest

ENDPOINT = "https://api.exa.ai/search"
MAX_RESULTS = 25  # Exa's lowest price band covers 1-25 results.
HIGHLIGHT_CHARS = 400


def build_request(query: str, limit: int, *, deep: bool, key: str) -> ApiRequest:
    return ApiRequest(
        url=ENDPOINT,
        headers={"x-api-key": key, "Content-Type": "application/json"},
        body={
            "query": query,
            "type": "auto" if deep else "fast",
            "numResults": max(1, min(limit, MAX_RESULTS)),
            "contents": {
                "highlights": {
                    "maxCharacters": HIGHLIGHT_CHARS,
                    "query": query,
                }
            },
        },
    )


def _snippet(result: dict[str, Any]) -> str | None:
    highlights = result.get("highlights")
    if isinstance(highlights, list):
        text = " ".join(h for h in highlights if isinstance(h, str))
        if text.strip():
            return text
    for key in ("summary", "text"):
        value = result.get(key)
        if isinstance(value, str) and value.strip():
            return value
    return None


def _date(value: Any) -> str | None:
    # "2024-05-01T00:00:00.000Z" -> "2024-05-01"
    return value[:10] if isinstance(value, str) and len(value) >= 10 else None


def to_rows(data: Any) -> list[ResultRow]:
    results = data.get("results") if isinstance(data, dict) else None
    rows = []
    for result in results if isinstance(results, list) else []:
        if not isinstance(result, dict):
            continue
        url = result.get("url")
        host = (urlsplit(url or "").hostname or "").removeprefix("www.")
        author = result.get("author") if isinstance(result.get("author"), str) else None
        rows.append(
            make_row(
                "exa",
                url=url,
                title=result.get("title"),
                snippet=_snippet(result),
                thumbnail_url=result.get("image"),
                meta=join_meta(host, author, _date(result.get("publishedDate"))),
            )
        )
    return keep(rows)
