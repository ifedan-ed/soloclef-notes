"""Build a result row from scraped fields, refusing anything without a web URL."""

from __future__ import annotations

from ..schemas import ResearchSource, ResultRow
from ..snippets import plain_text, trim
from ..urls import is_http_url, result_id


def make_row(
    source: ResearchSource,
    *,
    url: str | None,
    title: str | None,
    snippet: str | None = None,
    thumbnail_url: str | None = None,
    meta: str | None = None,
) -> ResultRow | None:
    if not is_http_url(url):
        return None
    url = url.strip()
    clean_title = trim(title, 300) or url
    return ResultRow(
        id=result_id(url),
        url=url,
        title=clean_title,
        snippet=trim(snippet),
        source=source,
        thumbnail_url=thumbnail_url if is_http_url(thumbnail_url) else None,
        meta=(plain_text(meta)[:120] or None) if meta else None,
    )


def keep(rows) -> list[ResultRow]:
    return [row for row in rows if row is not None]
