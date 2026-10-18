"""Collapse result rows that point at the same page."""

from __future__ import annotations

from .schemas import ResultRow
from .urls import is_http_url


def dedupe(rows: list[ResultRow]) -> list[ResultRow]:
    """First occurrence wins; rows whose URL is not http(s) are dropped.

    Row ids are derived from the normalized URL, so equal ids mean equal pages.
    """
    seen: set[str] = set()
    kept: list[ResultRow] = []
    for row in rows:
        if row.id in seen or not is_http_url(row.url):
            continue
        seen.add(row.id)
        kept.append(row)
    return kept
