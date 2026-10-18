"""Google Scholar: a recorded results page, pacing, caching and the wall."""

from __future__ import annotations

import pytest

from app.config import config
from app.notebooks.research.academic.clients import AcademicClients
from app.notebooks.research.academic.scholar import search as scholar_search
from app.notebooks.research.academic.scholar.parse import (
    hit_row,
    page_has_results,
    parse_results,
)
from app.notebooks.research.academic.search import search_academic
from app.notebooks.research.source_errors import SourceUnavailableError

from .academic_fixtures import fixture

pytestmark = pytest.mark.unit

SORRY_PAGE = (
    "<html><body>Our systems have detected unusual traffic from your computer "
    "network. <form action='/sorry/index'></form></body></html>"
)


@pytest.fixture(autouse=True)
def _fresh_scholar(monkeypatch):
    monkeypatch.setattr(config, "SCHOLAR_MIN_INTERVAL_S", 0.0)
    scholar_search.clear_cache()
    yield
    scholar_search.clear_cache()


def test_results_page_reads_title_byline_citations_and_pdf():
    hits = parse_results(fixture("scholar_bronchiolitis.html"))
    assert 8 <= len(hits) <= 10
    first = hits[0]
    assert first.title.startswith("Is treatment with a high flow nasal cannula")
    assert first.url == "https://link.springer.com/article/10.1007/s00134-013-2879-y"
    assert first.authors[:2] == ["C Milési", "J Baleine"]
    assert first.venue == "Intensive care"
    assert first.year == 2013
    assert first.cited_by == 279
    assert first.pdf_url == "https://hal.umontpellier.fr/hal-02544936/document"


def test_hit_row_finds_ids_in_links_and_marks_open_access():
    row = hit_row(parse_results(fixture("scholar_bronchiolitis.html"))[0])
    assert row.source == "google_scholar"
    assert row.paper.doi == "10.1007/s00134-013-2879-y"
    assert row.paper.badges == ["scholar"]
    assert row.paper.open_access is True
    assert "Cited by 279" in row.meta


def test_a_wall_is_not_a_results_page():
    assert page_has_results(fixture("scholar_bronchiolitis.html"))
    assert not page_has_results(SORRY_PAGE)


async def test_a_repeat_search_is_served_from_cache():
    calls = []

    async def fetch_page(url):
        calls.append(url)
        return fixture("scholar_bronchiolitis.html")

    clients = AcademicClients(fetch_scholar_page=fetch_page)
    first = await search_academic("google_scholar", "hfnc", deep=False, clients=clients)
    again = await search_academic(
        "google_scholar", "HFNC ", deep=False, clients=clients
    )
    assert len(calls) == 1
    assert "num=10" in calls[0]
    assert [r.url for r in again] == [r.url for r in first]


async def test_deep_reads_a_second_page_only_when_the_first_is_full():
    calls = []

    async def fetch_page(url):
        calls.append(url)
        return fixture("scholar_bronchiolitis.html")

    await search_academic(
        "google_scholar",
        "hfnc",
        deep=True,
        clients=AcademicClients(fetch_scholar_page=fetch_page),
    )
    # The recorded page has 10 hits for a 20-hit request: no second page.
    assert len(calls) == 1
    assert "num=20" in calls[0]


async def test_a_walled_search_says_so():
    async def walled(url):
        return None

    with pytest.raises(SourceUnavailableError) as raised:
        await search_academic(
            "google_scholar",
            "hfnc",
            deep=False,
            clients=AcademicClients(fetch_scholar_page=walled),
        )
    assert raised.value.code == "source_blocked"
    assert "Google Scholar" in raised.value.message


def test_searches_are_spaced_by_the_configured_interval(monkeypatch):
    monkeypatch.setattr(config, "SCHOLAR_MIN_INTERVAL_S", 10.0)
    pacer = scholar_search.HostPacer(lambda host: config.SCHOLAR_MIN_INTERVAL_S)
    assert pacer.reserve("scholar.google.com") == 0
    assert pacer.reserve("scholar.google.com") == pytest.approx(10.0, abs=0.1)
