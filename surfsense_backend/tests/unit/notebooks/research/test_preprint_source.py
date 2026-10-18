"""medRxiv/bioRxiv: Europe PMC for discovery, api.biorxiv.org for the record."""

from __future__ import annotations

import pytest

from app.notebooks.research.academic.clients import AcademicClients
from app.notebooks.research.academic.preprints import (
    parse_details,
    parse_search,
    pdf_from_jats_url,
    search_url,
)
from app.notebooks.research.academic.search import search_academic

from .academic_fixtures import ReplayFetch, fixture

pytestmark = pytest.mark.unit


def test_search_is_limited_to_the_two_preprint_servers():
    url = search_url('bronchiolitis "high flow"', 10)
    assert "SRC:PPR" in url
    assert "medRxiv" in url and "bioRxiv" in url
    assert "pageSize=10" in url


def test_search_results_are_preprints_with_doi_server_and_pdf():
    preprints = parse_search(fixture("epmc_preprints.json"))
    assert preprints
    first = preprints[0]
    assert first.doi == "10.1101/2020.12.10.20245662"
    assert first.server == "medrxiv"
    assert first.pdf_url.endswith(".full.pdf")
    assert first.year == 2020
    assert "<h4>" not in first.abstract
    assert first.authors[0] == "Gall ET"


async def test_rows_carry_a_server_badge_and_open_access():
    fetch = ReplayFetch((r"europepmc", fixture("epmc_preprints.json")))
    rows = await search_academic(
        "medrxiv", "x", deep=False, clients=AcademicClients(fetch_text=fetch)
    )
    first = rows[0]
    assert first.source == "medrxiv"
    assert first.url == "https://www.medrxiv.org/content/10.1101/2020.12.10.20245662"
    assert first.paper.badges == ["medrxiv"]
    assert first.paper.open_access is True


def test_details_read_the_latest_version_and_jats_link():
    details = parse_details(fixture("biorxiv_details.json"), "medrxiv")
    assert details.version == "1"
    assert details.jats_url.endswith(".source.xml")
    assert details.abstract.startswith("Objective")


def test_the_pdf_sits_beside_the_jats_under_the_server_name():
    assert pdf_from_jats_url(
        "medrxiv",
        "https://www.medrxiv.org/content/early/2020/12/14/2020.12.10.20245662.source.xml",
    ) == (
        "https://www.medrxiv.org/content/medrxiv/early/2020/12/14/"
        "2020.12.10.20245662.full.pdf"
    )
    assert pdf_from_jats_url("medrxiv", None) is None
