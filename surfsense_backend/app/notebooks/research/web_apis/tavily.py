"""Tavily search: https://docs.tavily.com/documentation/api-reference/endpoint/search.

Fast is a ``basic`` search (1 credit), deep an ``advanced`` one (2 credits)
with more results. Tavily's ``content`` is the snippet; no answer, images or
raw page content are asked for, since the page is read by SurfSense's own
crawler when imported.
"""

from __future__ import annotations

from typing import Any
from urllib.parse import urlsplit

from ..schemas import ResultRow
from ..snippets import join_meta
from ..sources.rows import keep, make_row
from .requests import ApiRequest

ENDPOINT = "https://api.tavily.com/search"
MAX_RESULTS = 20  # Tavily's ceiling for max_results.


def build_request(query: str, limit: int, *, deep: bool, key: str) -> ApiRequest:
    return ApiRequest(
        url=ENDPOINT,
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
        body={
            "query": query,
            "search_depth": "advanced" if deep else "basic",
            "max_results": max(1, min(limit, MAX_RESULTS)),
            "topic": "general",
            "include_answer": False,
            "include_raw_content": False,
            "include_images": False,
            "include_favicon": False,
        },
    )


def to_rows(data: Any) -> list[ResultRow]:
    results = data.get("results") if isinstance(data, dict) else None
    rows = []
    for result in results if isinstance(results, list) else []:
        if not isinstance(result, dict):
            continue
        url = result.get("url")
        host = (urlsplit(url or "").hostname or "").removeprefix("www.")
        published = result.get("published_date")
        rows.append(
            make_row(
                "tavily",
                url=url,
                title=result.get("title"),
                snippet=result.get("content"),
                meta=join_meta(host, published if isinstance(published, str) else None),
            )
        )
    return keep(rows)
