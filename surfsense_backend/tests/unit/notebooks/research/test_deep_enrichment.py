"""Deep research swaps search teasers for the page's own words, without breaking on bad pages."""

from __future__ import annotations

import pytest

from app.notebooks.research.enrichment import enrich, pages_worth_reading
from app.notebooks.research.schemas import ResultRow
from app.notebooks.research.urls import result_id

pytestmark = pytest.mark.unit

PAGE = "Solar panels turn sunlight into power with photovoltaic cells on the roof."


def _row(n: int, source: str = "web") -> ResultRow:
    url = f"https://site{n}.example/page"
    return ResultRow(
        id=result_id(url), url=url, title=f"r{n}", snippet="teaser", source=source
    )


async def _collect(rows, reader):
    events = [event async for event in enrich(rows, "solar panels", reader)]
    return events


async def test_reads_only_the_top_five_web_pages():
    rows = [_row(n) for n in range(8)]
    read: list[str] = []

    async def reader(url):
        read.append(url)
        return PAGE

    events = await _collect(rows, reader)
    assert [(done, total) for done, total, _ in events] == [(i, 5) for i in range(1, 6)]
    final = events[-1][2]
    assert [row.snippet for row in final[:5]] == [PAGE] * 5
    assert [row.snippet for row in final[5:]] == ["teaser"] * 3
    assert read == [row.url for row in rows[:5]]


async def test_failed_and_empty_pages_keep_their_teaser():
    rows = [_row(0), _row(1), _row(2)]

    async def reader(url):
        if "site0" in url:
            raise RuntimeError("blocked")
        if "site1" in url:
            return None
        return PAGE

    final = (await _collect(rows, reader))[-1][2]
    assert [row.snippet for row in final] == ["teaser", "teaser", PAGE]


def test_platform_posts_are_not_crawled():
    rows = [_row(0, "reddit"), _row(1, "web"), _row(2, "youtube")]
    assert pages_worth_reading(rows) == [1]


async def test_a_pages_og_image_becomes_its_thumbnail():
    rows = [_row(0), _row(1)]

    async def reader(url):
        if "site0" in url:
            return PAGE, "https://site0.example/og.jpg"
        return PAGE, "javascript:alert(1)"

    final = (await _collect(rows, reader))[-1][2]
    assert final[0].thumbnail_url == "https://site0.example/og.jpg"
    assert final[1].thumbnail_url is None
