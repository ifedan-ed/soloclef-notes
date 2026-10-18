"""Which URLs are importable, and which result rows are the same page."""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from app.notebooks.research.dedupe import dedupe
from app.notebooks.research.schemas import (
    ImportItem,
    ImportRequest,
    ResultRow,
    SearchRequest,
)
from app.notebooks.research.urls import normalize_url, result_id

pytestmark = pytest.mark.unit


def _row(url: str, title: str = "t") -> ResultRow:
    return ResultRow(id=result_id(url), url=url, title=title, snippet="", source="web")


@pytest.mark.parametrize(
    ("a", "b"),
    [
        ("https://www.example.com/a/", "http://example.com/a"),
        ("https://example.com/a?utm_source=x&b=2&a=1", "https://example.com/a?a=1&b=2"),
        ("https://EXAMPLE.com/a#section", "https://example.com/a"),
    ],
)
def test_urls_that_are_the_same_page_normalize_equal(a, b):
    assert normalize_url(a) == normalize_url(b)


def test_different_paths_stay_distinct():
    assert normalize_url("https://example.com/a") != normalize_url(
        "https://example.com/b"
    )


def test_dedupe_keeps_first_occurrence_and_drops_non_http():
    rows = [
        _row("https://www.example.com/a/", "first"),
        _row("http://example.com/a", "second"),
        _row("https://example.com/b"),
        ResultRow(
            id="x", url="javascript:alert(1)", title="t", snippet="", source="web"
        ),
    ]
    kept = dedupe(rows)
    assert [row.title for row in kept] == ["first", "t"]
    assert [row.url for row in kept][1] == "https://example.com/b"


@pytest.mark.parametrize(
    "url",
    [
        "ftp://example.com/f",
        "javascript:alert(1)",
        "file:///etc/passwd",
        "example.com",
        "",
    ],
)
def test_import_items_refuse_non_http_urls(url):
    with pytest.raises(ValidationError):
        ImportItem(url=url, title="x")


def test_import_item_accepts_and_trims_http():
    item = ImportItem(url="  https://example.com/a  ", title="  A  ")
    assert (item.url, item.title, item.source) == ("https://example.com/a", "A", "web")


def test_import_request_bounds():
    with pytest.raises(ValidationError):
        ImportRequest(items=[])
    with pytest.raises(ValidationError):
        ImportRequest(
            items=[{"url": f"https://e.com/{i}", "title": "t"} for i in range(51)]
        )


def test_search_request_strips_and_bounds_the_query():
    assert SearchRequest(query="  solar  ").query == "solar"
    with pytest.raises(ValidationError):
        SearchRequest(query="   ")
    with pytest.raises(ValidationError):
        SearchRequest(query="x" * 501)
    with pytest.raises(ValidationError):
        SearchRequest(query="solar", source="bing")
