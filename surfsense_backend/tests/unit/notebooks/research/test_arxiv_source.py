"""arXiv's Atom API, replayed from a recorded answer."""

from __future__ import annotations

import pytest

from app.notebooks.research.academic.arxiv import (
    entry_row,
    parse_feed,
    query_url,
    search_query,
)
from app.notebooks.research.academic.clients import AcademicClients
from app.notebooks.research.academic.search import search_academic

from .academic_fixtures import ReplayFetch, fixture

pytestmark = pytest.mark.unit


def test_every_query_word_must_appear():
    assert search_query("high-flow nasal cannula") == (
        "all:high-flow AND all:nasal AND all:cannula"
    )


def test_query_url_keeps_field_colons_and_ranks_by_relevance():
    url = query_url("nasal cannula", 10)
    assert "search_query=all:nasal%20AND%20all:cannula" in url
    assert "max_results=10" in url
    assert "sortBy=relevance" in url


def test_feed_entries_carry_id_version_authors_and_categories():
    entries = parse_feed(fixture("arxiv_query.xml"))
    assert len(entries) == 2
    first = entries[0]
    assert first.arxiv_id == "2111.11846"
    assert first.version == "v1"
    assert first.title.startswith("Predicting High-Flow Nasal Cannula Failure")
    assert first.authors
    assert first.published == "2021-11-20"
    assert first.categories


def test_entry_row_is_open_access_with_its_pdf():
    row = entry_row(parse_feed(fixture("arxiv_query.xml"))[0])
    assert row.url == "https://arxiv.org/abs/2111.11846"
    assert row.paper.arxiv_id == "2111.11846"
    assert row.paper.pdf_url == "https://arxiv.org/pdf/2111.11846v1"
    assert row.paper.badges == ["arxiv"]
    assert row.paper.open_access is True
    assert "2021" in row.meta


async def test_deep_asks_for_thirty():
    fetch = ReplayFetch((r"export\.arxiv\.org", fixture("arxiv_query.xml")))
    rows = await search_academic(
        "arxiv", "nasal cannula", deep=True, clients=AcademicClients(fetch_text=fetch)
    )
    assert len(rows) == 2
    assert "max_results=30" in fetch.urls[0]


EMPTY_FEED = (
    '<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">'
    "<title>arXiv Query</title></feed>"
)


async def test_no_paper_with_every_word_falls_back_to_any_word():
    fetch = ReplayFetch(
        (r"AND%20all", EMPTY_FEED),
        (r"OR%20all", fixture("arxiv_query.xml")),
    )
    rows = await search_academic(
        "arxiv",
        "bronchiolitis nasal cannula",
        deep=False,
        clients=AcademicClients(fetch_text=fetch),
    )
    assert len(rows) == 2
    assert "AND" in fetch.urls[0] and "OR" in fetch.urls[1]


async def test_a_one_word_query_is_asked_once():
    fetch = ReplayFetch((r"export\.arxiv\.org", EMPTY_FEED))
    rows = await search_academic(
        "arxiv", "bronchiolitis", deep=False, clients=AcademicClients(fetch_text=fetch)
    )
    assert rows == []
    assert len(fetch.urls) == 1
